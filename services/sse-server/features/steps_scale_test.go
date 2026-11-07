package features

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cucumber/godog"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/sse-server/internal/stream"
)

func contextBG() context.Context { return context.Background() }

// Steps for the integration scenarios: several servers, a real Redis, many customers.
func registerScaleSteps(sc *godog.ScenarioContext, W func() *world) {
	sc.Step(`^(\d+) sse-servers with (\d+) customers each connected to "([^"]*)"$`, func(servers, each int, id string) error {
		w := W()
		var all []*client
		for i := 0; i < servers; i++ {
			srv := w.newServer(nil)
			cs, err := w.connectMany(srv, id, each)
			if err != nil {
				return err
			}
			all = append(all, cs...)
		}
		w.captured["all"] = all
		return nil
	})
	sc.Step(`^all (\d+) receive it in under 1 second, with identical bytes$`, func(n int) error {
		w := W()
		all := w.captured["all"].([]*client)
		deadline := w.lastPublish.Add(time.Second)
		var want string
		for _, c := range all {
			for !hasBroadcast(c) {
				if time.Now().After(deadline) {
					return fmt.Errorf("a customer did not receive the broadcast within 1 s")
				}
				time.Sleep(2 * time.Millisecond)
			}
			got := lastData(c)
			if want == "" {
				want = got
			}
			if got != want {
				return fmt.Errorf("different bytes:\n%s\n%s", got, want)
			}
		}
		if len(all) != n {
			return fmt.Errorf("%d customers, want %d", len(all), n)
		}
		return nil
	})
	sc.Step(`^all (\d+) receive "status", then "sold-out", and their responses end$`, func(n int) error {
		w := W()
		all := w.captured["all"].([]*client)
		if len(all) != n {
			return fmt.Errorf("%d customers, want %d", len(all), n)
		}
		for _, c := range all {
			if err := w.expectClosedSequence(c); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^(\d+) customers are connected on a real Redis cluster$`, func(n int) error {
		w := W()
		cs, err := w.connectMany(w.srv(), "E1", n)
		w.captured["all"] = cs
		return err
	})
	sc.Step(`^the Redis node is restarted$`, func() error {
		w := W()
		cc, ok := w.rdb.(*goredis.ClusterClient)
		if !ok {
			return fmt.Errorf("needs a Redis cluster")
		}
		err := cc.ForEachMaster(contextBG(), func(ctx context.Context, c *goredis.Client) error {
			return c.Do(ctx, "CLIENT", "KILL", "TYPE", "pubsub").Err()
		})
		time.Sleep(time.Second)
		return err
	})
	sc.Step(`^all (\d+) receive "stale" redis_disconnected exactly once and stay connected$`, func(n int) error {
		for _, c := range W().captured["all"].([]*client) {
			if countStale(c, "true") != 1 || !c.open() {
				return fmt.Errorf("stale=%d open=%v", countStale(c, "true"), c.open())
			}
		}
		return nil
	})
	sc.Step(`^all (\d+) receive "stale" false, then the broadcast$`, func(n int) error {
		time.Sleep(200 * time.Millisecond)
		for _, c := range W().captured["all"].([]*client) {
			if countStale(c, "false") != 1 {
				return fmt.Errorf("a customer did not receive stale false")
			}
		}
		return nil
	})

	sc.Step(`^(\d+) customers are connected to "([^"]*)" on one server$`, func(n int, id string) error {
		w := W()
		srv := w.srv()
		if err := w.ensureEvent(id); err != nil {
			return err
		}
		pipe := w.rdb.Pipeline()
		toks := make([]string, n)
		for i := range toks {
			toks[i] = fmt.Sprintf("qt_load%05d", i)
			pipe.HSet(contextBG(), "token:"+toks[i], "event_id", id, "ticket_no", 60000+i)
		}
		if _, err := pipe.Exec(contextBG()); err != nil {
			return err
		}
		var cs []*client
		for i, tok := range toks {
			cs = append(cs, w.launch(srv, id, tok, fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255)))
		}
		deadline := time.Now().Add(20 * time.Second)
		for srv.Connections(id) < n+1 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if got := srv.Connections(id); got < n+1 {
			return fmt.Errorf("%d connections registered, want %d", got, n+1)
		}
		w.captured["load"] = cs
		return nil
	})
	sc.Step(`^(\d+) broadcasts are published one second apart$`, func(n int) error {
		w := W()
		load := w.captured["load"].([]*client)
		var lat []time.Duration
		for i := 0; i < n; i++ {
			before := make([]int64, len(load))
			for j, c := range load {
				before[j] = c.w.writes.Load()
			}
			t0 := time.Now()
			if err := w.publishBroadcast("E1", 31601+int64(i), ""); err != nil {
				return err
			}
			var last int64
			deadline := t0.Add(5 * time.Second)
			for done := 0; done < len(load) && time.Now().Before(deadline); {
				done = 0
				last = 0
				for j, c := range load {
					if c.w.writes.Load() > before[j] {
						done++
						if lw := c.w.lastWrite.Load(); lw > last {
							last = lw
						}
					}
				}
				time.Sleep(time.Millisecond)
			}
			lat = append(lat, time.Duration(last-t0.UnixNano()))
			time.Sleep(time.Second)
		}
		w.captured["latencies"] = lat
		return nil
	})
	sc.Step(`^the p95 from PUBLISH to the last byte read is under 1 second$`, func() error {
		lat := append([]time.Duration(nil), W().captured["latencies"].([]time.Duration)...)
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		p95 := lat[(len(lat)*95)/100]
		if p95 >= time.Second || p95 <= 0 {
			return fmt.Errorf("p95 = %s (latencies %v)", p95, lat)
		}
		return nil
	})
}

// launch starts a stream handler without waiting for it to answer (for large crowds).
func (w *world) launch(srv *stream.Server, id, token, ip string) *client {
	c := w.openAsync(srv, id, token, ip)
	return c
}

func hasBroadcast(c *client) bool { return strings.Count(c.w.body(), "data: ") >= 2 }

func lastData(c *client) string {
	fs := nonComment(c.frames())
	for i := len(fs) - 1; i >= 0; i-- {
		if fs[i].event == "" {
			return fs[i].data
		}
	}
	return ""
}
