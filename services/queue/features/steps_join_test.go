package features

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/cucumber/godog"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/delivery"
)

func registerJoinSteps(sc *godog.ScenarioContext, W func() *world) {
	join := func(r joinReq) error { W().record(W().do(r)); return nil }

	sc.Step(`^a guest with device id "([^"]*)" joins "([^"]*)" with captcha "([^"]*)"$`, func(dev, id, c string) error {
		return join(joinReq{eventID: id, deviceID: dev, captcha: ptr(c)})
	})
	sc.Step(`^a guest with device id "([^"]*)" joins event "([^"]*)" with captcha "([^"]*)"$`, func(dev, id, c string) error {
		return join(joinReq{eventID: id, deviceID: dev, captcha: ptr(c)})
	})
	sc.Step(`^a guest without a device id joins "([^"]*)" with captcha "([^"]*)"$`, func(id, c string) error {
		return join(joinReq{eventID: id, noDevice: true, captcha: ptr(c)})
	})
	sc.Step(`^a guest from IP "([^"]*)" joins "([^"]*)" with captcha "([^"]*)"$`, func(ip, id, c string) error {
		return join(joinReq{eventID: id, ip: ip, captcha: ptr(c)})
	})
	sc.Step(`^a guest joins "([^"]*)"$`, func(id string) error {
		w := W()
		r, ok := w.captured["gatereq"].(joinReq)
		if !ok {
			r = joinReq{eventID: id}
		}
		return join(r)
	})
	sc.Step(`^a guest joins "([^"]*)" sending queue_token "([^"]*)" with captcha "([^"]*)"$`, func(id, qt, c string) error {
		W().captured["presented"] = qt
		return join(joinReq{eventID: id, queueToken: qt, captcha: ptr(c)})
	})
	sc.Step(`^the guest joins "([^"]*)" sending queue_token "([^"]*)" with captcha "([^"]*)"$`, func(id, qt, c string) error {
		W().captured["presented"] = qt
		return join(joinReq{eventID: id, queueToken: qt, captcha: ptr(c), deviceID: "dev-gggggggggggggggg"})
	})
	sc.Step(`^the guest joins "([^"]*)" again from the same device sending queue_token "([^"]*)" as (the queue_token cookie|the X-Queue-Token header) with captcha "([^"]*)"$`,
		func(id, qt, carrier, c string) error {
			dev, _ := W().captured["guestDevice"].(string)
			r := joinReq{eventID: id, deviceID: dev, queueToken: qt, captcha: ptr(c), carrier: "cookie"}
			if strings.Contains(carrier, "header") {
				r.carrier = "header"
			}
			return join(r)
		})
	sc.Step(`^a guest with device id "([^"]*)" posts body (".*") to join "([^"]*)"$`, func(dev, quoted, id string) error {
		body, err := strconv.Unquote(quoted)
		if err != nil {
			return fmt.Errorf("body %s: %w", quoted, err)
		}
		return join(joinReq{eventID: id, deviceID: dev, body: &body})
	})
	sc.Step(`^a guest posts a 1 MB body to join "([^"]*)"$`, func(id string) error {
		body := `{"captcha_token": "` + strings.Repeat("a", 1<<20) + `"}`
		return join(joinReq{eventID: id, body: &body})
	})
	sc.Step(`^a caller with (an expired access token|a malformed access token) and device id "([^"]*)" joins "([^"]*)" with captcha "([^"]*)"$`,
		func(kind, dev, id, c string) error {
			tok := "not-a.jwt"
			if strings.Contains(kind, "expired") {
				tok = expiredToken("acc-1")
			}
			return join(joinReq{eventID: id, deviceID: dev, bearer: tok, captcha: ptr(c)})
		})
	sc.Step(`^a guest sends "X-Forwarded-For: ([^"]*)" and joins "([^"]*)"$`, func(xff, id string) error {
		return W().joinViaGateway(id, xff)
	})

	// ---- accounts
	acctJoin := func(a, id, c, dev string) error {
		w := W()
		tok, err := w.accessToken(a)
		if err != nil {
			return err
		}
		r := joinReq{eventID: id, bearer: tok, captcha: ptr(c), noDevice: dev == ""}
		r.deviceID = dev
		return join(r)
	}
	sc.Step(`^account "([^"]*)" without a device id joins "([^"]*)" with captcha "([^"]*)"$`, func(a, id, c string) error { return acctJoin(a, id, c, "") })
	sc.Step(`^account "([^"]*)" joins "([^"]*)" with captcha "([^"]*)"$`, func(a, id, c string) error { return acctJoin(a, id, c, "") })
	sc.Step(`^account "([^"]*)" joins "([^"]*)" again with captcha "([^"]*)"$`, func(a, id, c string) error { return acctJoin(a, id, c, "") })
	sc.Step(`^account "([^"]*)" joins "([^"]*)" again from another device with captcha "([^"]*)"$`, func(a, id, c string) error {
		return acctJoin(a, id, c, "dev-bbbbbbbbbbbbbbbb")
	})
	sc.Step(`^account "([^"]*)" makes (\d+) join attempts within 1 minute from \d+ different IPs$`, func(a string, n int) error {
		w := W()
		tok, err := w.accessToken(a)
		if err != nil {
			return err
		}
		for i := range n {
			w.record(w.do(joinReq{eventID: "E1", bearer: tok, noDevice: true, ip: fmt.Sprintf("198.51.100.%d", i+1)}))
		}
		return nil
	})
	sc.Step(`^account "([^"]*)" makes a 4th attempt from a 4th IP$`, func(a string) error {
		w := W()
		tok, err := w.accessToken(a)
		if err != nil {
			return err
		}
		w.record(w.do(joinReq{eventID: "E1", bearer: tok, noDevice: true, ip: "198.51.100.4"}))
		return nil
	})
	sc.Step(`^account "([^"]*)" sends (\d+) joins for "([^"]*)" at the same time$`, func(a string, n int, id string) error {
		w := W()
		tok, err := w.accessToken(a)
		if err != nil {
			return err
		}
		w.parallel(n, n, func(int) joinReq { return joinReq{eventID: id, bearer: tok, noDevice: true} })
		return nil
	})

	// ---- rate limit sequences
	sc.Step(`^a guest from IP "([^"]*)" makes (\d+) join attempts within 1 minute$`, func(ip string, n int) error {
		w := W()
		for range n {
			w.record(w.do(joinReq{eventID: "E1", ip: ip}))
		}
		w.captured["ip"] = ip
		return nil
	})
	sc.Step(`^the same IP makes a 21st attempt$`, func() error {
		w := W()
		ip, _ := w.captured["ip"].(string)
		w.record(w.do(joinReq{eventID: "E1", ip: ip}))
		return nil
	})

	// ---- crowds
	sc.Step(`^(\d+) guests with distinct device ids join "([^"]*)" in parallel from (\d+) workers$`, func(n int, id string, workers int) error {
		W().parallel(n, workers, func(i int) joinReq { return joinReq{eventID: id, deviceID: fmt.Sprintf("dev-%016d", i)} })
		return nil
	})
	sc.Step(`^(\d+) guests join "([^"]*)" in parallel from (\d+) workers$`, func(n int, id string, workers int) error {
		W().parallel(n, workers, func(i int) joinReq { return joinReq{eventID: id, deviceID: fmt.Sprintf("dev-%016d", i)} })
		return nil
	})
	sc.Step(`^guests "([^"]*)", "([^"]*)", "([^"]*)" join "([^"]*)" one after another$`, func(a, b, c, id string) error {
		w := W()
		for _, g := range []string{a, b, c} {
			resp := w.do(joinReq{eventID: id, deviceID: "dev-guest-" + g + "-0000000"})
			w.record(resp)
			w.captured["guest:"+g] = resp
		}
		return nil
	})
	sc.Step(`^two guests join "([^"]*)" at the same time$`, func(id string) error {
		W().parallel(2, 2, func(i int) joinReq { return joinReq{eventID: id, deviceID: fmt.Sprintf("dev-two-%012d", i)} })
		return nil
	})
	sc.Step(`^a guest joins "([^"]*)" and the join dies$`, func(id string) error { return join(joinReq{eventID: id, deviceID: "dev-dies-000000000"}) })
	sc.Step(`^another guest joins "([^"]*)"$`, func(id string) error { return join(joinReq{eventID: id, deviceID: "dev-another-0000000"}) })

	// ---- cancellation
	sc.Step(`^the client cancels the request after (\d+) ms$`, func(ms int) error {
		w := W()
		ctx, cancel := context.WithCancel(context.Background())
		after := time.Duration(ms) * time.Millisecond
		w.captured["cancelAt"] = time.Now().Add(after)
		time.AfterFunc(after, cancel)
		// An empty queue: ticket_no == now_serving + 1, so join waits.
		snap, err := w.snapshot("E1")
		if err != nil {
			return err
		}
		if err := w.setCounters("E1", snap.Issued, snap.Issued); err != nil {
			return err
		}
		w.record(w.do(joinReq{eventID: "E1", ctx: ctx}))
		w.captured["returnedAt"] = time.Now()
		return nil
	})

	// ---- Lambda vs local server
	sc.Step(`^the join request goes through the API Gateway Lambda proxy adapter$`, func() error { return W().lambdaAndServer(true) })
	sc.Step(`^the same request goes through the local HTTP server$`, func() error { return W().lambdaAndServer(false) })
}

// expiredToken signs an HS256 token whose exp is in the past.
func expiredToken(sub string) string {
	enc := base64.RawURLEncoding.EncodeToString
	h, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	c, _ := json.Marshal(map[string]any{"sub": sub, "iat": time.Now().Add(-2 * time.Hour).Unix(), "exp": time.Now().Add(-time.Hour).Unix()})
	in := enc(h) + "." + enc(c)
	m := hmac.New(sha256.New, []byte(testJWTSecret))
	m.Write([]byte(in))
	return in + "." + enc(m.Sum(nil))
}

func (w *world) gatewayEvent(id, body string, headers map[string]string, sourceIP string) events.APIGatewayV2HTTPRequest {
	path := "/events/" + id + "/queue/join"
	return events.APIGatewayV2HTTPRequest{
		Version:  "2.0",
		RouteKey: "POST /events/{event_id}/queue/join",
		RawPath:  path,
		Headers:  headers,
		Body:     body,
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: http.MethodPost, Path: path, SourceIP: sourceIP},
		},
	}
}

// joinViaGateway sends the join as API Gateway would, with a spoofed XFF.
func (w *world) joinViaGateway(id, xff string) error {
	ip, _ := w.captured["gwip"].(string)
	w.limiter = &countingLimiter{}
	h := w.app()
	evt := w.gatewayEvent(id, `{"captcha_token": "cap-ok"}`, map[string]string{
		"content-type": "application/json", "x-device-id": defaultDevice, "x-forwarded-for": xff,
	}, ip)
	start := time.Now()
	out, err := httpadapter.NewV2(h).ProxyWithContext(context.Background(), evt)
	if err != nil {
		return err
	}
	w.record(&response{status: out.StatusCode, body: []byte(out.Body), header: headerOf(out), took: time.Since(start)})
	return nil
}

func headerOf(out events.APIGatewayV2HTTPResponse) http.Header {
	h := http.Header{}
	for k, v := range out.Headers {
		h.Set(k, v)
	}
	for k, vs := range out.MultiValueHeaders {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	for _, c := range out.Cookies {
		h.Add("Set-Cookie", c)
	}
	return h
}

// lambdaAndServer sends one join through the production wiring
// (delivery.Wire), first as a Lambda proxy event, then as a plain HTTP request.
func (w *world) lambdaAndServer(lambda bool) error {
	srv, ok := w.captured["wired"].(*delivery.Server)
	if !ok {
		cfg := config.Config{Env: "local", JWTSecret: testJWTSecret, Captcha: config.CaptchaMock, SSEBaseURL: testSSEBase, Join: w.cfg}
		var err error
		srv, err = delivery.Wire(cfg, delivery.Clients{Redis: w.appRdb, Metrics: w.metrics, Clock: w.clock})
		if err != nil {
			return err
		}
		w.captured["wired"] = srv
	}
	body := `{"captcha_token": "cap-ok"}`
	if lambda {
		evt := w.gatewayEvent("E1", body, map[string]string{"content-type": "application/json", "x-device-id": defaultDevice}, defaultIP)
		out, err := httpadapter.NewV2(srv.Handler).ProxyWithContext(context.Background(), evt)
		if err != nil {
			return err
		}
		w.record(&response{status: out.StatusCode, body: []byte(out.Body), header: headerOf(out)})
		return nil
	}
	req := httptest.NewRequest(http.MethodPost, "/events/E1/queue/join", strings.NewReader(body))
	req.Header.Set("X-Device-Id", defaultDevice)
	req.RemoteAddr = defaultIP + ":40000"
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	w.record(&response{status: rec.Code, body: rec.Body.Bytes(), header: rec.Header()})
	return nil
}

// ticketOf resolves a response's ticket number, also for a "granted" body
// (which has no ticket_no) via its token hash.
func (w *world) ticketOf(r *response) (int64, error) {
	d, err := r.data()
	if err != nil {
		return 0, err
	}
	if n, ok := d["ticket_no"].(float64); ok {
		return int64(n), nil
	}
	qt, _ := d["queue_token"].(string)
	v, err := w.rdb.HGet(context.Background(), "token:"+qt, "ticket_no").Result()
	if err == goredis.Nil {
		return 0, fmt.Errorf("no ticket for %s", qt)
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}
