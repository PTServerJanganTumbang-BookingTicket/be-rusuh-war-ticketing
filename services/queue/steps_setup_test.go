package queue_test

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/cucumber/godog"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/idgen"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	queueredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/redis"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/turnstile"
)

func registerSetupSteps(sc *godog.ScenarioContext, W func() *world) {
	// ---- event seeding
	sc.Step(`^event "([^"]*)" named "([^"]*)" is "([^"]*)"$`, func(id, name, state string) error {
		return W().seedEvent(id, state, nil, nil, name)
	})
	sc.Step(`^event "([^"]*)" is "([^"]*)" with now_serving (\d+) and issued (\d+)$`, func(id, state string, ns, issued int64) error {
		return W().seedEvent(id, state, &ns, &issued, "")
	})
	sc.Step(`^event "([^"]*)" is "([^"]*)"$`, func(id, state string) error { return W().setState(id, state) })
	sc.Step(`^event "([^"]*)" is now "([^"]*)"$`, func(id, state string) error { return W().setState(id, state) })
	sc.Step(`^event "([^"]*)" was "([^"]*)" and is now "([^"]*)"$`, func(id, was, now string) error {
		if err := W().setState(id, was); err != nil {
			return err
		}
		return W().setState(id, now)
	})
	sc.Step(`^event "([^"]*)" is "([^"]*)" with sale_starts_at "([^"]*)"$`, func(id, state, at string) error {
		w := W()
		if err := w.setState(id, state); err != nil {
			return err
		}
		return w.rdb.HSet(context.Background(), ev(id).QueueHash(), queueredis.FieldSaleStartsAt, at).Err()
	})
	sc.Step(`^event "([^"]*)" is "([^"]*)" with sold_out_at "([^"]*)"$`, func(id, state, at string) error {
		w := W()
		if err := w.setState(id, state); err != nil {
			return err
		}
		return w.rdb.Set(context.Background(), ev(id).SoldOutAt(), at, 0).Err()
	})
	sc.Step(`^event "([^"]*)" has no state key$`, func(id string) error {
		return W().rdb.Del(context.Background(), ev(id).QueueState()).Err()
	})
	sc.Step(`^event "([^"]*)" has the unknown state "([^"]*)"$`, func(id, state string) error {
		return W().rdb.Set(context.Background(), ev(id).QueueState(), state, 0).Err()
	})
	sc.Step(`^now_serving is (\d+) and issued is (\d+)$`, func(ns, issued int64) error {
		return W().setCounters("E1", ns, issued)
	})
	sc.Step(`^the queue is now full$`, func() error {
		w := W()
		snap, err := w.snapshot("E1")
		if err != nil {
			return err
		}
		w.cfg.Capacity = snap.Depth()
		return nil
	})

	// ---- configuration
	sc.Step(`^QUEUE_CAPACITY is (\d+)$`, func(n int64) error { W().cfg.Capacity = n; return nil })
	sc.Step(`^QUEUE_CAPACITY is (\d+) and SALE_WINDOW_SECONDS is (\d+)$`, func(n, s int64) error {
		w := W()
		w.cfg.Capacity = n
		w.cfg.SaleWindow = time.Duration(s) * time.Second
		w.opt.CookieMaxAge = w.cfg.SaleWindow
		return nil
	})
	sc.Step(`^JOIN_GRANT_WAIT_MS is (\d+)$`, func(ms int) error {
		W().cfg.GrantWait = time.Duration(ms) * time.Millisecond
		return nil
	})
	sc.Step(`^COOKIE_DOMAIN is "([^"]*)"$`, func(d string) error { W().opt.CookieDomain = d; return nil })
	sc.Step(`^the clock is frozen at "([^"]*)"$`, func(ts string) error {
		at, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return err
		}
		W().freeze(at)
		return nil
	})
	sc.Step(`^rate limits are disabled for this test$`, func() error { W().cfg.RateLimits = false; return nil })

	// ---- CAPTCHA
	sc.Step(`^the CAPTCHA provider accepts token "([^"]*)"$`, func(tok string) error {
		W().fcaptcha.accept[tok] = true
		return nil
	})
	sc.Step(`^the CAPTCHA provider accepts token "([^"]*)" and rejects token "([^"]*)"$`, func(ok, bad string) error {
		w := W()
		w.fcaptcha.accept[ok] = true
		w.fcaptcha.reject[bad] = true
		return nil
	})
	sc.Step(`^the CAPTCHA provider (times out after the 1 s deadline|answers HTTP 500|is unreachable|answers a body that is not JSON)$`, func(string) error {
		W().fcaptcha.unavailable = true
		return nil
	})
	sc.Step(`^the CAPTCHA adapter is the mock(?: that accepts every token)?$`, func() error {
		W().captcha = turnstile.Mock{}
		return nil
	})

	// ---- faults
	sc.Step(`^the rate limiter errors$`, func() error { W().limiter = &countingLimiter{fail: true}; return nil })
	sc.Step(`^Redis is unreachable$`, func() error { W().redisBad = true; return nil })
	sc.Step(`^Postgres fails every query$`, func() error { W().bookings = failingBookings{}; return nil })
	sc.Step(`^the join will crash after INCR, before HSET$`, func() error {
		w := W()
		w.app()
		w.store.crashOnSave.Store(true)
		return nil
	})

	// ---- gate order
	sc.Step(`^the request fails gate "([^"]*)"$`, func(g string) error { W().gateFail = g; return nil })
	sc.Step(`^every later gate would also fail$`, func() error { return W().failGatesFrom(W().gateFail) })

	// ---- identity and rate limits
	sc.Step(`^account "([^"]*)" has a valid access token$`, func(a string) error {
		_, err := W().accessToken(a)
		return err
	})
	sc.Step(`^IP "([^"]*)" has used its (\d+) joins(?: in the last 5 minutes)?$`, func(ip string, n int) error {
		w := W()
		w.usedIPs[ip] = true
		w.captured["ip"] = ip
		lim := ratelimit.New(w.rdb)
		for range n {
			if _, err := lim.Allow(context.Background(), rediskeys.JoinRateLimit(ip), w.cfg.IPLimit); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^(\d+) seconds pass$`, func(s int) error {
		w := W()
		ip, _ := w.captured["ip"].(string)
		if ip == "" {
			return fmt.Errorf("no rate-limited IP in this scenario")
		}
		return w.rdb.HIncrBy(context.Background(), rediskeys.JoinRateLimit(ip), ratelimit.FieldTimestamp,
			-(time.Duration(s) * time.Second).Microseconds()).Err()
	})
	sc.Step(`^the gateway reports source IP "([^"]*)"$`, func(ip string) error {
		W().captured["gwip"] = ip
		W().usedIPs[ip] = true
		return nil
	})

	// ---- engagements created "earlier"
	sc.Step(`^account "([^"]*)" joined "([^"]*)" earlier with ticket number (\d+) and queue_token "([^"]*)"$`,
		func(a, id string, n int64, qt string) error { return W().seedEngagement(id, qt, n, a, true, true) })
	sc.Step(`^account "([^"]*)" joined "([^"]*)" earlier$`, func(a, id string) error {
		return W().seedEngagement(id, "qt_A", 32748, a, true, true)
	})
	sc.Step(`^account "([^"]*)" joined "([^"]*)" earlier and got ticket (\d+)$`, func(a, id string, n int64) error {
		return W().seedEngagement(id, "qt_A", n, a, true, true)
	})
	sc.Step(`^a guest joined "([^"]*)" earlier with ticket number (\d+) and queue_token "([^"]*)"$`, func(id string, n int64, qt string) error {
		return W().seedEngagement(id, qt, n, "", true, false)
	})
	sc.Step(`^a guest holds queue_token "([^"]*)" for event "([^"]*)"$`, func(qt, id string) error {
		w := W()
		if err := w.seedEvent(id, "OPEN", ptr(int64(0)), ptr(int64(5)), ""); err != nil {
			return err
		}
		return w.seedEngagement(id, qt, 5, "", true, false)
	})
	sc.Step(`^account "([^"]*)" has token "([^"]*)" with ticket number (\d+) but it is not in by_ticket$`, func(a, qt string, n int64) error {
		return W().seedEngagement("E1", qt, n, a, false, true)
	})
	sc.Step(`^acct:\{([^}]*)\}:(\S+) points to "([^"]*)" but token:(\S+) does not exist$`, func(id, a, qt, _ string) error {
		w := W()
		if _, err := w.accessToken(a); err != nil {
			return err
		}
		return w.rdb.Set(context.Background(), ev(id).Acct(a), qt, time.Hour).Err()
	})
	sc.Step(`^account "([^"]*)" holds ticket number (\d+) with booking status "([^"]*)"$`, func(a string, n int64, status string) error {
		w := W()
		if err := w.seedEngagement("E1", "qt_B", n, a, false, true); err != nil {
			return err
		}
		return w.insertBooking("E1", "qt_B", status)
	})
	sc.Step(`^account "([^"]*)" had booking status "([^"]*)" for "([^"]*)"$`, func(a, status, id string) error {
		w := W()
		if err := w.seedEngagement(id, "qt_B", 31000, a, false, true); err != nil {
			return err
		}
		return w.insertBooking(id, "qt_B", status)
	})

	// ---- stand-in admission controller (until step 3 lands)
	sc.Step(`^the stand-in controller grants the next joiner after (\d+) ms$`, func(ms int) error {
		return W().startStandIn(time.Duration(ms)*time.Millisecond, 1)
	})
	sc.Step(`^the stand-in controller grants both joiners after (\d+) ms$`, func(ms int) error {
		return W().startStandIn(time.Duration(ms)*time.Millisecond, 2)
	})
	sc.Step(`^the stand-in controller does not grant$`, func() error { return nil })
}

func ptr[T any](v T) *T { return &v }

func (w *world) setState(id, state string) error {
	return w.rdb.Set(context.Background(), ev(id).QueueState(), state, 0).Err()
}

func (w *world) setCounters(id string, ns, issued int64) error {
	ctx := context.Background()
	e := ev(id)
	if err := w.rdb.HSet(ctx, e.QueueHash(), queueredis.FieldNowServing, ns).Err(); err != nil {
		return err
	}
	return w.rdb.Set(ctx, e.QueueIssued(), issued, 0).Err()
}

func (w *world) seedEvent(id, state string, ns, issued *int64, name string) error {
	ctx := context.Background()
	e := ev(id)
	if err := w.setState(id, state); err != nil {
		return err
	}
	if name == "" {
		name = "Event " + id
	}
	if err := w.rdb.HSet(ctx, e.QueueHash(), queueredis.FieldName, name).Err(); err != nil {
		return err
	}
	if ns != nil {
		return w.setCounters(id, *ns, *issued)
	}
	return nil
}

func (w *world) snapshot(id string) (domain.EventSnapshot, error) {
	return queueredis.NewStore(w.rdb).Snapshot(context.Background(), ev(id))
}

// seedEngagement writes a place as an earlier join would have.
func (w *world) seedEngagement(id, qt string, ticketNo int64, account string, inLine, acct bool) error {
	ctx := context.Background()
	e := ev(id)
	eng := domain.Engagement{QueueToken: qt, EventID: id, TicketNo: ticketNo, AccountID: account, JoinedAt: time.Now().UTC()}
	if account == "" {
		eng.DeviceHash = "seeded-device-hash"
	}
	if err := queueredis.NewStore(w.rdb).SaveEngagement(ctx, eng, w.cfg.SaleWindow); err != nil {
		return err
	}
	if inLine {
		if err := w.rdb.ZAdd(ctx, e.ByTicket(), goredis.Z{Score: float64(ticketNo), Member: qt}).Err(); err != nil {
			return err
		}
	}
	if account != "" {
		if _, err := w.accessToken(account); err != nil {
			return err
		}
		if acct {
			if err := w.rdb.Set(ctx, e.Acct(account), qt, w.cfg.SaleWindow).Err(); err != nil {
				return err
			}
		}
	}
	w.presentQ = qt
	return nil
}

func (w *world) accessToken(account string) (string, error) {
	if t, ok := w.accounts[account]; ok {
		return t, nil
	}
	t, _, err := w.issuer.Issue(account, account+"@example.com", true)
	if err != nil {
		return "", err
	}
	w.accounts[account] = t
	w.bearer = t
	return t, nil
}

func (w *world) needDB() error {
	if !w.integration {
		return fmt.Errorf("this step needs Postgres: tag the scenario @integration")
	}
	return testDB()
}

func (w *world) insertBooking(eventID, qt, status string) error {
	if err := w.needDB(); err != nil {
		return err
	}
	_, err := adminDB.Exec(context.Background(), `INSERT INTO bookings
		(id, event_id, queue_token, status, grant_token, granted_at, booking_expires_at, contact_email, terms_version, terms_accepted_at)
		VALUES ($1, $2, $3, $4, $5, now(), now() + interval '5 minutes', 'buyer@example.com', 'v1', now())`,
		idgen.NewPrefixed("RB-"), eventID, qt, status, idgen.NewPrefixed("gt_"))
	return err
}

// failGatesFrom makes gate g and every gate after it fail.
func (w *world) failGatesFrom(g string) error {
	order := []string{"request shape", "identity", "rate limit", "CAPTCHA required", "CAPTCHA", "sale state", "sold out", "capacity"}
	idx := -1
	for i, name := range order {
		if name == g {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("unknown gate %q", g)
	}
	r := joinReq{eventID: "E1"}
	for _, name := range order[idx:] {
		switch name {
		case "request shape":
			r.body = ptr("not json")
		case "identity":
			r.noDevice = true
		case "rate limit":
			w.limiter = &countingLimiter{refuse: true}
		case "CAPTCHA required":
			r.captcha = ptr("")
		case "CAPTCHA":
			if r.captcha == nil {
				r.captcha = ptr("cap-bad")
			}
		case "sale state":
			if err := w.setState("E1", "SCHEDULED"); err != nil {
				return err
			}
		case "sold out":
			if g == "sold out" {
				if err := w.setState("E1", "SOLD_OUT"); err != nil {
					return err
				}
			}
		case "capacity":
			w.cfg.Capacity = 49
		}
	}
	w.captured["gatereq"] = r
	return nil
}

// standIn plays admission-ctl until step 3 exists: it writes the GRANTED row
// for joiners at the head of an empty queue, after a delay.
type standIn struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	grants map[string]grantRow // by queue_token
}

type grantRow struct {
	grantToken string
	expiresAt  time.Time
}

func (w *world) startStandIn(delay time.Duration, count int) error {
	if err := w.needDB(); err != nil {
		return err
	}
	snap, err := w.snapshot("E1")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &standIn{cancel: cancel, grants: map[string]grantRow{}}
	w.stand = s
	e := ev("E1")
	for i := range count {
		ticket := snap.NowServing + 1 + int64(i)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for {
				members, err := w.rdb.ZRangeByScore(ctx, e.ByTicket(), &goredis.ZRangeBy{
					Min: strconv.FormatInt(ticket, 10), Max: strconv.FormatInt(ticket, 10),
				}).Result()
				if err == nil && len(members) > 0 {
					select {
					case <-ctx.Done():
						return
					case <-time.After(delay):
					}
					g := grantRow{grantToken: idgen.NewPrefixed("gt_"), expiresAt: time.Now().Add(5 * time.Minute).UTC().Truncate(time.Second)}
					_, err := adminDB.Exec(ctx, `INSERT INTO bookings (id, event_id, queue_token, status, grant_token, granted_at, booking_expires_at)
						VALUES ($1, 'E1', $2, 'GRANTED', $3, now(), $4)`, idgen.NewPrefixed("RB-"), members[0], g.grantToken, g.expiresAt)
					if err == nil {
						s.mu.Lock()
						s.grants[members[0]] = g
						s.mu.Unlock()
					}
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
		}()
	}
	return nil
}

func (s *standIn) stop() {
	s.cancel()
	s.wg.Wait()
}

func (s *standIn) grant(qt string) (grantRow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.grants[qt]
	return g, ok
}
