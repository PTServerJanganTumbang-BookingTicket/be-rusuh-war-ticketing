package features

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

type sample struct {
	name   string
	status int
	body   string
}

func registerContractSteps(sc *godog.ScenarioContext, W func() *world) {
	sc.Step(`^the server produces 401, 404, 409, 410, 429 and 503 responses and every stream message type$`, func() error {
		return W().produceSamples()
	})
	sc.Step(`^each validates against the Apidog schema$`, func() error { return W().validateSamples() })
}

// produceSamples drives the server into every documented answer and message.
func (w *world) produceSamples() error {
	var errs []sample
	add := func(name string, c *client) {
		errs = append(errs, sample{name: name, status: c.w.code(), body: c.w.body()})
	}

	w.srv()
	if err := w.seedEvent("E1", "OPEN", 31600, 318); err != nil {
		return err
	}
	if err := w.seedToken(myToken, "E1", myTicket, ""); err != nil {
		return err
	}
	add("401", w.open("E1", openOpts{}))
	add("404", w.open("E9", openOpts{cookie: myToken}))
	if err := w.seedEvent("E2", "SCHEDULED", 0, 0); err != nil {
		return err
	}
	add("409", w.open("E2", openOpts{cookie: myToken}))
	if err := w.seedEvent("E3", "CLOSED", 0, 0); err != nil {
		return err
	}
	add("410", w.open("E3", openOpts{cookie: myToken}))
	for i := 0; i < 5; i++ {
		if _, err := w.limiter.Allow(contextBG(), rediskeys.SSERateLimit("203.0.113.9"), w.cfg.IPLimit); err != nil {
			return err
		}
	}
	add("429", w.open("E1", openOpts{cookie: myToken, ip: "203.0.113.9"}))
	down := w.newServer(failStore{})
	add("503", w.open("E1", openOpts{cookie: myToken, srv: down}))

	// every stream message type, from one connected customer
	me := w.open("E1", openOpts{cookie: myToken, ip: "10.9.9.9"})
	w.me = me
	if err := w.publishBroadcast("E1", 31604, ""); err != nil {
		return err
	}
	if err := w.publishStatus("E1", "FULL_BOOKED"); err != nil {
		return err
	}
	w.settle()
	w.pass(31 * time.Second)
	if err := w.publishStatus("E1", "CLOSED"); err != nil {
		return err
	}
	w.settle()
	frames := me.frames()
	// reconnect comes from a server that is shutting down
	other := w.newServer(nil)
	rc := w.open("E1", openOpts{cookie: myToken, ip: "10.9.9.8", srv: other})
	go func() { _ = other.Drain(contextBG()) }()
	w.settle()
	frames = append(frames, rc.frames()...)

	w.captured["samples"] = errs
	w.captured["frames"] = frames
	return nil
}

func (w *world) validateSamples() error {
	api, err := loadStreamAPI()
	if err != nil {
		return err
	}
	var problems []string
	byCode := map[int]any{}
	var itemSchema any
	for _, r := range api["responses"].([]any) {
		rm := r.(map[string]any)
		if rm["contentType"] == "eventStream" {
			itemSchema = rm["itemSchema"]
			continue
		}
		code, _ := strconv.Atoi(fmt.Sprint(rm["code"]))
		byCode[code] = rm["jsonSchema"]
	}
	for _, s := range w.captured["samples"].([]sample) {
		sch, ok := byCode[s.status]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: Apidog has no %d response", s.name, s.status))
			continue
		}
		if err := validate(sch, s.body); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", s.name, err))
		}
	}
	seen := map[string]bool{}
	for _, f := range w.captured["frames"].([]frame) {
		if f.comment || f.data == "" {
			continue
		}
		name := f.event
		if name == "" {
			name = "default"
		}
		seen[name] = true
		if err := validate(itemSchema, `{"data":`+f.data+`}`); err != nil {
			problems = append(problems, fmt.Sprintf("event %s %s: %v", name, f.data, err))
		}
	}
	for _, want := range []string{"default", "status", "stale", "sold-out", "reconnect"} {
		if !seen[want] {
			problems = append(problems, "no sample of the "+want+" message")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

func validate(schema any, doc string) error {
	c := jsonschema.NewCompiler()
	if err := c.AddResource("s.json", schema); err != nil {
		return err
	}
	compiled, err := c.Compile("s.json")
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(doc))
	if err != nil {
		return fmt.Errorf("not JSON: %q", truncate(doc))
	}
	return compiled.Validate(inst)
}

func loadStreamAPI() (map[string]any, error) {
	path := filepath.Join("..", "..", "..", "..", "documentations", "api", "rusuh-booking-apidog.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Apidog file (documentations repo next to this one): %w", err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	var found map[string]any
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if api, ok := t["api"].(map[string]any); ok && api["path"] == "/events/{event_id}/queue/stream" {
				found = api
				return
			}
			for _, x := range t {
				walk(x)
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(doc)
	if found == nil {
		return nil, fmt.Errorf("stream endpoint not found in Apidog")
	}
	return found, nil
}
