package emf

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEMF_writesOneLinePerPoint(t *testing.T) {
	var buf bytes.Buffer
	m := New("Rusuh/Test", &buf)
	m.Incr("RateLimitFailOpen")
	m.Record("CaptchaFailOpenSeconds", 1.25)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines: %q", len(lines), buf.String())
	}
	for i, want := range []struct {
		name, unit string
		value      float64
	}{{"RateLimitFailOpen", "Count", 1}, {"CaptchaFailOpenSeconds", "Seconds", 1.25}} {
		var got map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &got); err != nil {
			t.Fatal(err)
		}
		if got[want.name] != want.value {
			t.Errorf("%s = %v", want.name, got[want.name])
		}
		aws := got["_aws"].(map[string]any)
		cw := aws["CloudWatchMetrics"].([]any)[0].(map[string]any)
		metric := cw["Metrics"].([]any)[0].(map[string]any)
		if cw["Namespace"] != "Rusuh/Test" || metric["Unit"] != want.unit {
			t.Errorf("EMF block %v", cw)
		}
	}
}
