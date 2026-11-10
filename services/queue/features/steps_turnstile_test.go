package features

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cucumber/godog"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/turnstile"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/port/outbound"
)

type siteverify struct {
	srv     *httptest.Server
	mu      sync.Mutex
	handler http.HandlerFunc
	form    url.Values
}

type tsState struct {
	fake    *siteverify
	secret  string
	timeout time.Duration
	result  domain.CaptchaResult
	took    time.Duration
}

func registerTurnstileSteps(sc *godog.ScenarioContext, W func() *world) {
	var ts *tsState
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		ts = &tsState{timeout: time.Second}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if ts.fake != nil {
			ts.fake.srv.CloseClientConnections()
			ts.fake.srv.Close()
		}
		return ctx, err
	})
	answer := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}
	}
	setHandler := func(h http.HandlerFunc) error {
		if ts.fake == nil {
			return errors.New("no fake siteverify server")
		}
		ts.fake.mu.Lock()
		ts.fake.handler = h
		ts.fake.mu.Unlock()
		return nil
	}

	sc.Step(`^a fake siteverify server$`, func() error {
		f := &siteverify{}
		f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			f.mu.Lock()
			f.form = r.PostForm
			h := f.handler
			f.mu.Unlock()
			if h == nil {
				h = answer(200, `{"success": true}`)
			}
			h(w, r)
		}))
		ts.fake = f
		return nil
	})
	sc.Step(`^TURNSTILE_SECRET_KEY is "([^"]*)"$`, func(s string) error { ts.secret = s; return nil })
	sc.Step(`^the siteverify timeout is (\d+) s$`, func(s int) error { ts.timeout = time.Duration(s) * time.Second; return nil })
	sc.Step(`^siteverify answers (\{.*\})$`, func(body string) error { return setHandler(answer(200, body)) })
	sc.Step(`^siteverify sleeps for (\d+) s$`, func(s int) error {
		return setHandler(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(time.Duration(s) * time.Second):
			case <-r.Context().Done():
			}
			answer(200, `{"success": true}`)(w, r)
		})
	})
	sc.Step(`^siteverify answers HTTP (\d+)$`, func(code int) error { return setHandler(answer(code, `{"success": true}`)) })
	sc.Step(`^siteverify closes the connection$`, func() error {
		return setHandler(func(w http.ResponseWriter, _ *http.Request) {
			hj, ok := w.(http.Hijacker)
			if !ok {
				return
			}
			c, _, err := hj.Hijack()
			if err == nil {
				_ = c.Close()
			}
		})
	})
	sc.Step(`^siteverify answers "([^"]*)"$`, func(body string) error { return setHandler(answer(200, body)) })

	verify := func(tok, ip string) error {
		if ts.fake == nil {
			return errors.New("no fake siteverify server")
		}
		c := turnstile.New(ts.fake.srv.URL, ts.secret, ts.timeout, W().metrics)
		start := time.Now()
		ts.result = c.Verify(context.Background(), tok, ip)
		ts.took = time.Since(start)
		return nil
	}
	sc.Step(`^the adapter verifies token "([^"]*)" for client IP "([^"]*)"$`, verify)
	sc.Step(`^the adapter verifies token "([^"]*)"$`, func(tok string) error { return verify(tok, "") })
	sc.Step(`^the result is "([^"]*)"$`, func(want string) error {
		if ts.result.String() != want {
			return fmt.Errorf("result = %s, want %s", ts.result, want)
		}
		return nil
	})
	sc.Step(`^the result is "([^"]*)" within ([\d.]+) s$`, func(want string, s float64) error {
		if ts.result.String() != want {
			return fmt.Errorf("result = %s, want %s", ts.result, want)
		}
		if ts.took > time.Duration(s*float64(time.Second)) {
			return fmt.Errorf("verify took %v, want ≤ %vs", ts.took, s)
		}
		return nil
	})
	sc.Step(`^siteverify received secret "([^"]*)", response "([^"]*)" and remoteip "([^"]*)"$`, func(secret, resp, ip string) error {
		ts.fake.mu.Lock()
		defer ts.fake.mu.Unlock()
		f := ts.fake.form
		if f.Get("secret") != secret || f.Get("response") != resp || f.Get("remoteip") != ip {
			return fmt.Errorf("siteverify received secret=%q response=%q remoteip=%q", f.Get("secret"), f.Get("response"), f.Get("remoteip"))
		}
		return nil
	})

	// ---- mock adapter
	sc.Step(`^it verifies (\d+) tokens$`, func(n int) error {
		var calls atomic.Int64
		prev := http.DefaultTransport
		http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("network disabled in this test")
		})
		defer func() { http.DefaultTransport = prev }()
		var c outbound.Captcha = turnstile.Mock{}
		if W().captcha != nil {
			c = W().captcha
		}
		for i := range n {
			if r := c.Verify(context.Background(), fmt.Sprintf("tok-%d", i), "203.0.113.7"); r != domain.CaptchaValid {
				return fmt.Errorf("mock returned %s", r)
			}
		}
		W().captured["netcalls"] = calls.Load()
		return nil
	})
	sc.Step(`^no network call was made$`, func() error {
		if n, _ := W().captured["netcalls"].(int64); n != 0 {
			return fmt.Errorf("%d network calls", n)
		}
		return nil
	})

	// ---- secret handling
	sc.Step(`^no file in the repository contains a Turnstile secret$`, noTurnstileSecretInRepo)
	sc.Step(`^the service fails to start in a non-local environment without TURNSTILE_SECRET_KEY$`, func() error {
		cfg := config.Config{Env: "preprod", DatabaseURL: "postgres://x", JWTSecret: "x", Captcha: config.CaptchaTurnstile}
		cfg.Join.Capacity, cfg.Join.SaleWindow, cfg.Join.DevicePepper = 1, time.Hour, []byte("p")
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "TURNSTILE_SECRET_KEY") {
			return fmt.Errorf("Validate() = %v, want a TURNSTILE_SECRET_KEY error", err)
		}
		return nil
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Turnstile secret keys start with "0x4AAAAAAA"; built by concatenation so
// this file does not match itself.
var turnstileSecret = regexp.MustCompile("0x4" + "AAAAAAA" + `[A-Za-z0-9_-]{16,}`)

func noTurnstileSecretInRepo() error {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		return err
	}
	var hits []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".bin", "node_modules", "coverage", ".aws-sam":
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 2<<20 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if turnstileSecret.Match(b) {
			rel, _ := filepath.Rel(root, p)
			hits = append(hits, rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(hits) > 0 {
		return fmt.Errorf("Turnstile secret found in: %v", hits)
	}
	return nil
}
