package features

import (
	"context"
	"fmt"
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
	sc.Step(`^event "([^"]*)" is "([^"]*)" with full_booked_at "([^"]*)"$`, func(id, state, at string) error {
		w := W()
		if err := w.setState(id, state); err != nil {
			return err
		}
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return err
		}
		// stored as epoch seconds (ADR-006 §8.12); join shows it as ISO 8601
		return w.rdb.HSet(context.Background(), ev(id).QueueHash(), queueredis.FieldFullBookedAt, t.Unix()).Err()
	})
	sc.Step(`^event "([^"]*)" has no state key$`, func(id string) error {
		return W().rdb.HDel(context.Background(), ev(id).QueueHash(), queueredis.FieldState).Err()
	})
	sc.Step(`^event "([^"]*)" has the unknown state "([^"]*)"$`, func(id, state string) error {
		return W().setState(id, state)
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
		return nil
	})
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
}

func ptr[T any](v T) *T { return &v }

func (w *world) setState(id, state string) error {
	return w.rdb.HSet(context.Background(), ev(id).QueueHash(), queueredis.FieldState, state).Err()
}

func (w *world) setCounters(id string, ns, issued int64) error {
	ctx := context.Background()
	e := ev(id)
	return w.rdb.HSet(ctx, e.QueueHash(), queueredis.FieldNowServing, ns, queueredis.FieldIssued, issued).Err()
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
	dh := ""
	if account == "" {
		dh = w.hashDevice(defaultDevice) // rejoinable from the default test device
	}
	return w.seedEngagementOn(id, qt, ticketNo, account, dh, inLine, acct)
}

// seedEngagementOn is seedEngagement with an explicit device_hash ("" = none).
func (w *world) seedEngagementOn(id, qt string, ticketNo int64, account, deviceHash string, inLine, acct bool) error {
	ctx := context.Background()
	e := ev(id)
	eng := domain.Engagement{QueueToken: qt, EventID: id, TicketNo: ticketNo, AccountID: account, DeviceHash: deviceHash, JoinedAt: time.Now().UTC()}
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
		(id, event_id, queue_token, status, grant_token, granted_at, booking_expires_at, contact_email)
		VALUES ($1, $2, $3, $4, $5, now(), now() + interval '5 minutes', 'buyer@example.com')`,
		idgen.NewPrefixed("RB-"), eventID, qt, status, idgen.NewPrefixed("gt_"))
	return err
}

// failGatesFrom makes gate g and every gate after it fail.
func (w *world) failGatesFrom(g string) error {
	order := []string{"request shape", "identity", "rate limit", "CAPTCHA required", "CAPTCHA", "sale state", "fully booked", "capacity"}
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
		case "fully booked":
			if g == "fully booked" {
				if err := w.setState("E1", "FULL_BOOKED"); err != nil {
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
