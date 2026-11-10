package apinizer_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/specsuite"
)

func TestFeatures(t *testing.T) { specsuite.Run(t, initScenario) }

type world struct {
	rec *httptest.ResponseRecorder
}

func initScenario(sc *godog.ScenarioContext) {
	w := &world{}
	restore := func() {}
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		restore()
		return ctx, err
	})
	sc.Step(`^the clock is frozen at "([^"]*)"$`, func(ts string) error {
		at, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			return err
		}
		restore = apinizer.SetClock(func() time.Time { return at })
		return nil
	})
	sc.Step(`^a handler writes status (\d+), message "([^"]*)" and data (.+)$`, w.writeData)
	sc.Step(`^a handler writes the app error (\d+) "([^"]*)" "([^"]*)" with data (.+)$`, w.writeAppError)
	sc.Step(`^a handler writes status (\d+) with nil data$`, func(status int) error {
		w.rec = httptest.NewRecorder()
		apinizer.WriteEnvelope(w.rec, status, "Success", nil)
		return nil
	})
	sc.Step(`^a handler returns a plain Go error "([^"]*)"$`, func(msg string) error {
		w.rec = httptest.NewRecorder()
		apinizer.WriteError(w.rec, errors.New(msg))
		return nil
	})
	sc.Step(`^the body is:$`, w.bodyIs)
	sc.Step(`^the Content-Type is "([^"]*)"$`, func(want string) error {
		if got := w.rec.Header().Get("Content-Type"); got != want {
			return fmt.Errorf("Content-Type = %q, want %q", got, want)
		}
		return nil
	})
	sc.Step(`^data is \{\}$`, w.dataIsEmptyObject)
	sc.Step(`^the status is (\d+) with error_code "([^"]*)"$`, w.statusWithCode)
	sc.Step(`^the message does not contain "([^"]*)"$`, w.messageLacks)
	sc.Step(`^the booking service test suite runs against the new envelope$`, func() error { return nil })
	sc.Step(`^it passes, and its responses now also carry meta\.server_time$`, bookingSuitePasses)
}

func decodeJSON(s string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("invalid JSON %q: %w", s, err)
	}
	return v, nil
}

func (w *world) writeData(status int, message, data string) error {
	v, err := decodeJSON(data)
	if err != nil {
		return err
	}
	w.rec = httptest.NewRecorder()
	apinizer.WriteEnvelope(w.rec, status, message, v)
	return nil
}

func (w *world) writeAppError(status int, code, message, data string) error {
	v, err := decodeJSON(data)
	if err != nil {
		return err
	}
	w.rec = httptest.NewRecorder()
	apinizer.WriteError(w.rec, apperror.New(status, code, message).WithCode(code).WithData(v))
	return nil
}

func (w *world) body() (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(w.rec.Body.Bytes(), &m); err != nil {
		return nil, fmt.Errorf("body %q is not a JSON object: %w", w.rec.Body.String(), err)
	}
	return m, nil
}

func (w *world) bodyIs(doc *godog.DocString) error {
	want, err := decodeJSON(doc.Content)
	if err != nil {
		return err
	}
	got, err := w.body()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(any(got), want) {
		return fmt.Errorf("body = %s\nwant   %s", w.rec.Body.String(), strings.TrimSpace(doc.Content))
	}
	return nil
}

func (w *world) dataIsEmptyObject() error {
	got, err := w.body()
	if err != nil {
		return err
	}
	d, ok := got["data"].(map[string]any)
	if !ok || len(d) != 0 {
		return fmt.Errorf("data = %#v, want {}", got["data"])
	}
	return nil
}

func (w *world) meta() (map[string]any, error) {
	got, err := w.body()
	if err != nil {
		return nil, err
	}
	m, ok := got["meta"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("no meta object in %s", w.rec.Body.String())
	}
	return m, nil
}

func (w *world) statusWithCode(status int, code string) error {
	if w.rec.Code != status {
		return fmt.Errorf("status = %d, want %d", w.rec.Code, status)
	}
	m, err := w.meta()
	if err != nil {
		return err
	}
	if m["error_code"] != code {
		return fmt.Errorf("meta.error_code = %v, want %q", m["error_code"], code)
	}
	return nil
}

func (w *world) messageLacks(s string) error {
	if strings.Contains(w.rec.Body.String(), s) {
		return fmt.Errorf("response leaks %q: %s", s, w.rec.Body.String())
	}
	return nil
}

// bookingSuitePasses runs the booking module's tests, which (via its HTTP
// test) assert meta.server_time on its responses.
func bookingSuitePasses() error {
	dir, err := filepath.Abs(filepath.Join("..", "..", "services", "booking"))
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return fmt.Errorf("booking module not found at %s: %w", dir, err)
	}
	args := []string{"test", "-count=1"}
	if testing.Short() {
		args = append(args, "-short")
	}
	cmd := exec.Command("go", append(args, "./...")...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("booking tests failed: %w\n%s", err, out)
	}
	return nil
}
