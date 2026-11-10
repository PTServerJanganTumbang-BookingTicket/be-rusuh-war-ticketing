package features

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/infrastructure/postgres"
)

func parseTime(ts string) (time.Time, error) { return time.Parse(time.RFC3339, ts) }

func (w *tickWorld) statusOf(name string) (string, error) {
	r, ok, err := w.rowOf(name)
	if err != nil || !ok {
		return "", fmt.Errorf("no row for %s (err %v)", name, err)
	}
	return r.Status, nil
}

// sweep runs SWEEP once, turning an injected crash into w.crashErr like runTick does.
func (w *tickWorld) sweep() {
	w.crashErr = nil
	func() {
		defer func() {
			if r := recover(); r != nil {
				c, ok := r.(crashed)
				if !ok {
					panic(r)
				}
				w.crashErr = fmt.Errorf("controller died %s", c.at)
			}
		}()
		w.scratch["lapsed"] = w.tick.Sweep(w.ctx)
	}()
}

func registerSweepSteps(sc *godog.ScenarioContext, w *tickWorld) {
	sc.Step(`^customer "c1" was granted booking "B1" with deadline "([^"]*)"$`, func(deadline string) error {
		d, err := parseTime(deadline)
		if err != nil {
			return err
		}
		w.clock.set(d.Add(-5 * time.Minute)) // the booking TTL is 5 minutes
		if _, err := w.join("E1", "c1", false); err != nil {
			return err
		}
		w.runTickLogged()
		if w.lastErr != nil {
			return w.lastErr
		}
		r, ok, err := w.rowOf("c1")
		if err != nil || !ok {
			return fmt.Errorf("c1 was not granted (err %v)", err)
		}
		w.scratch["B1"], w.scratch["G1"] = r.ID, r.GrantToken
		return nil
	})
	sc.Step(`^"B1" is still "([A-Z_]+)"$`, func(want string) error {
		got, err := w.statusOf("c1")
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("B1 is %s, want %s", got, want)
		}
		return nil
	})
	sc.Step(`^"B1" has status "([A-Z_]+)"$`, func(want string) error {
		got, err := w.statusOf("c1")
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("B1 has status %s, want %s", got, want)
		}
		return nil
	})
	sc.Step(`^the clock (?:reaches|reads) "([^"]*)" and SWEEP runs$`, func(ts string) error {
		t, err := parseTime(ts)
		if err != nil {
			return err
		}
		w.clock.set(t)
		w.sweep()
		return nil
	})
	sc.Step(`^the clock reaches "([^"]*)" and SWEEP runs and the controller dies$`, func(ts string) error {
		t, err := parseTime(ts)
		if err != nil {
			return err
		}
		w.clock.set(t)
		w.sweep()
		if w.crashErr == nil {
			return errors.New("the controller did not die")
		}
		return nil
	})
	sc.Step(`^the deadline passes and the queue SWEEP runs$`, func() error {
		w.clock.add(time.Hour)
		w.sweep()
		return nil
	})
	sc.Step(`^grant:<G1> no longer exists$`, func() error {
		n, err := w.rdb.Exists(w.ctx, rediskeys.Grant(w.scratch["G1"].(string))).Result()
		if err != nil || n != 0 {
			return fmt.Errorf("grant hash exists=%d err=%v", n, err)
		}
		return nil
	})
	sc.Step(`^grant:<G1> is gone$`, func() error {
		n, _ := w.rdb.Exists(w.ctx, rediskeys.Grant(w.scratch["G1"].(string))).Result()
		if n != 0 {
			return errors.New("the grant hash still exists")
		}
		return nil
	})
	sc.Step(`^grant:<G1> still exists$`, func() error {
		n, err := w.rdb.Exists(w.ctx, rediskeys.Grant(w.scratch["G1"].(string))).Result()
		if err != nil || n != 1 {
			return fmt.Errorf("grant hash exists=%d err=%v", n, err)
		}
		return nil
	})

	sc.Step(`^MAX_CONCURRENT_SLOTS is (\d+) and "B1" is the only active booking$`, func(n int64) error {
		w.reconfig(func(c *usecase.Config) { c.MaxSlots = n })
		return nil
	})
	sc.Step(`^customer "c2" is waiting$`, func() error { _, err := w.join("E1", "c2", false); return err })
	sc.Step(`^"B1" lapses and the next tick runs$`, func() error {
		w.clock.add(5*time.Minute + time.Second)
		w.runTickLogged()
		return w.lastErr
	})
	sc.Step(`^"c2" is granted$`, func() error {
		if st, err := w.statusOf("c2"); err != nil || st != "GRANTED" {
			return fmt.Errorf("c2 status %q err %v", st, err)
		}
		return nil
	})

	sc.Step(`^"B1" is "LAPSED"$`, func() error {
		w.clock.add(6 * time.Minute)
		w.sweep()
		return nil
	})
	var moveErr error
	sc.Step(`^any component tries to move "B1" to another status$`, func() error {
		moveErr = w.bks.moveOne(w.ctx, w.evID("E1"), "GRANTED", "SELECTING")
		return nil
	})
	sc.Step(`^the update changes 0 rows$`, func() error {
		if moveErr == nil {
			return errors.New("the update changed a row")
		}
		return nil
	})
	sc.Step(`^every use of G1 is rejected, because the booking is "LAPSED"$`, func() error {
		if st, err := w.statusOf("c1"); err != nil || st != "LAPSED" {
			return fmt.Errorf("booking status %q (err %v): the credential check would let G1 through", st, err)
		}
		return nil
	})
	sc.Step(`^"B1" is "GRANTED" with booking_expires_at moved to "([^"]*)"$`, func(ts string) error {
		t, err := parseTime(ts)
		if err != nil {
			return err
		}
		return w.bks.setDeadline(w.ctx, w.scratch["B1"].(string), t)
	})
	sc.Step(`^"B1" is "GRANTED" and past its deadline$`, func() error {
		w.clock.add(6 * time.Minute)
		return nil
	})
	sc.Step(`^sweepers A and B run SWEEP at the same time$`, func() error {
		var wg sync.WaitGroup
		got := make([]int, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				got[i] = usecase.NewTick(w.cfg, w.spyB, w.spyQ, w.clock, w.metrics).Sweep(w.ctx)
			}(i)
		}
		wg.Wait()
		w.scratch["sweepers"] = got
		return nil
	})
	sc.Step(`^exactly one of them is given "B1" back by the UPDATE$`, func() error {
		got := w.scratch["sweepers"].([]int)
		if got[0]+got[1] != 1 {
			return fmt.Errorf("sweepers got %v rows, want exactly 1 in total", got)
		}
		return nil
	})

	sc.Step(`^(\d+) bookings exist, of which (\d+) are "GRANTED"$`, func(total, granted int) error {
		_, err := w.bks.(*pgBookings).db.admin.Exec(w.ctx, `
INSERT INTO bookings (id, event_id, queue_token, status, grant_token, contact_email, booking_expires_at)
SELECT 'sw-' || i, 'X' || (i % 20), 'sw-q' || i, CASE WHEN i <= $1 THEN 'GRANTED' ELSE 'CONFIRMED' END,
       'sw-g' || i, 'a@example.com', now() + interval '1 day'
FROM generate_series(1, $2) AS i`, granted, total)
		if err != nil {
			return err
		}
		_, err = w.bks.(*pgBookings).db.admin.Exec(w.ctx, "ANALYZE bookings")
		return err
	})
	var plan string
	sc.Step(`^I EXPLAIN the sweep query$`, func() error {
		conn, err := w.bks.(*pgBookings).db.admin.Acquire(w.ctx)
		if err != nil {
			return err
		}
		defer conn.Release()
		var sb strings.Builder
		for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
			if _, err := conn.Exec(w.ctx, "SET plan_cache_mode = "+mode); err != nil {
				return err
			}
			rows, err := conn.Query(w.ctx, "EXPLAIN "+postgres.SweepSQL, time.Now())
			if err != nil {
				return err
			}
			sb.WriteString("-- " + mode + "\n")
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					rows.Close()
					return err
				}
				sb.WriteString(line + "\n")
			}
			rows.Close()
		}
		plan = sb.String()
		return nil
	})
	sc.Step(`^the plan uses the partial index on booking_expires_at for GRANTED rows$`, func() error {
		if strings.Count(plan, "bookings_granted_deadline_idx") < 2 {
			return fmt.Errorf("the plans do not use bookings_granted_deadline_idx:\n%s", plan)
		}
		return nil
	})

	// ---- the rejoin question
	sc.Step(`^"B1" lapsed for account "acc-1"$`, func() error {
		c := w.customers["c1"]
		if err := w.rdb.HSet(w.ctx, w.key("E1").Acct(c.account), "queue_token", c.token).Err(); err != nil {
			return err
		}
		w.scratch["acct"] = w.key("E1").Acct(c.account)
		w.scratch["issued0"], _ = w.field("E1", rediskeys.FieldIssued)
		w.clock.add(6 * time.Minute)
		w.sweep()
		return nil
	})
	sc.Step(`^acct:\{E1\}:acc-1 and token:<c1> still exist$`, func() error {
		for _, k := range []string{w.scratch["acct"].(string), rediskeys.Token(w.customers["c1"].token)} {
			if n, _ := w.rdb.Exists(w.ctx, k).Result(); n != 1 {
				return fmt.Errorf("%s was deleted by the lapse", k)
			}
		}
		return nil
	})
	sc.Step(`^issued is unchanged$`, func() error {
		got, _ := w.field("E1", rediskeys.FieldIssued)
		if got != w.scratch["issued0"] {
			return fmt.Errorf("issued = %s, was %v", got, w.scratch["issued0"])
		}
		return nil
	})
}
