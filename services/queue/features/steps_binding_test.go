package features

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

// Steps for join_token_binding.feature: a presented queue_token rejoins only
// for its owner (guest: same device; logged in: same account).
func registerBindingSteps(sc *godog.ScenarioContext, W func() *world) {
	join := func(r joinReq) error {
		W().captured["presented"] = r.queueToken
		W().record(W().do(r))
		return nil
	}

	// ---- places created "earlier"
	sc.Step(`^a guest with device id "([^"]*)" joined "([^"]*)" earlier with ticket number (\d+) and queue_token "([^"]*)"$`,
		func(dev, id string, n int64, qt string) error {
			w := W()
			w.captured["guestDevice"] = dev
			return w.seedEngagementOn(id, qt, n, "", w.hashDevice(dev), true, false)
		})
	sc.Step(`^account "([^"]*)" joined "([^"]*)" earlier from device "([^"]*)" with ticket number (\d+) and queue_token "([^"]*)"$`,
		func(a, id, dev string, n int64, qt string) error {
			w := W()
			return w.seedEngagementOn(id, qt, n, a, w.hashDevice(dev), true, true)
		})
	sc.Step(`^guest token "([^"]*)" for "([^"]*)" with ticket number (\d+) has no device_hash field$`, func(qt, id string, n int64) error {
		return W().seedEngagementOn(id, qt, n, "", "", true, false)
	})

	// ---- joins presenting a token
	sc.Step(`^a guest with device id "([^"]*)" joins "([^"]*)" sending queue_token "([^"]*)" with captcha "([^"]*)"$`,
		func(dev, id, qt, c string) error {
			return join(joinReq{eventID: id, deviceID: dev, queueToken: qt, captcha: ptr(c)})
		})
	sc.Step(`^a guest from IP "([^"]*)" with device id "([^"]*)" joins "([^"]*)" sending queue_token "([^"]*)" with captcha "([^"]*)"$`,
		func(ip, dev, id, qt, c string) error {
			return join(joinReq{eventID: id, ip: ip, deviceID: dev, queueToken: qt, captcha: ptr(c)})
		})
	sc.Step(`^account "([^"]*)" with device id "([^"]*)" joins "([^"]*)" sending queue_token "([^"]*)" with captcha "([^"]*)"$`,
		func(a, dev, id, qt, c string) error {
			tok, err := W().accessToken(a)
			if err != nil {
				return err
			}
			return join(joinReq{eventID: id, bearer: tok, deviceID: dev, queueToken: qt, captcha: ptr(c)})
		})

	// ---- assertions
	sc.Step(`^token:(\S+) field device_hash is still HMAC-SHA256\(DEVICE_PEPPER, "([^"]*)"\) hex$`, func(qt, dev string) error {
		w := W()
		got, err := w.rdb.HGet(context.Background(), rediskeys.Token(qt), "device_hash").Result()
		if err != nil {
			return fmt.Errorf("token:%s device_hash: %w", qt, err)
		}
		if got != w.hashDevice(dev) {
			return fmt.Errorf("token:%s device_hash is not the hash of %q", qt, dev)
		}
		return nil
	})
	sc.Step(`^acct:\{([^}]*)\}:(\S+) holds "([^"]*)"$`, func(id, a, qt string) error { return W().acctIs(id, a, qt) })
	sc.Step(`^the response clears cookie "([^"]*)" with Path=(\S+)$`, func(name, path string) error {
		r, err := W().last()
		if err != nil {
			return err
		}
		c, err := r.cookie(name)
		if err != nil {
			return err
		}
		if c.Value != "" || c.MaxAge >= 0 || c.Path != path {
			return fmt.Errorf("cookie %s = %q, Max-Age %d, Path %q; want cleared on %s", name, c.Value, c.MaxAge, c.Path, path)
		}
		return nil
	})
}

// hashDevice is the expected device_hash, computed here independently of the
// production code: hex(HMAC-SHA256(DEVICE_PEPPER, device id)).
func (w *world) hashDevice(dev string) string {
	m := hmac.New(sha256.New, w.cfg.DevicePepper)
	m.Write([]byte(dev))
	return hex.EncodeToString(m.Sum(nil))
}
