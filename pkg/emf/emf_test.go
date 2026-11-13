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

func decodeLine(t *testing.T, line string) (map[string]any, map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatal(err)
	}
	cw := got["_aws"].(map[string]any)["CloudWatchMetrics"].([]any)[0].(map[string]any)
	return got, cw
}

func TestAnEventLineCountsPerEventAndInTheTotal(t *testing.T) {
	var buf bytes.Buffer
	New("Rusuh/Queue", &buf).IncrEvent("QueueJoined", "evt-123")

	got, cw := decodeLine(t, strings.TrimSpace(buf.String()))
	if got["EventId"] != "evt-123" || got["QueueJoined"] != float64(1) {
		t.Errorf("line fields = %v", got)
	}
	dims, _ := json.Marshal(cw["Dimensions"])
	if string(dims) != `[["EventId"],[]]` {
		t.Errorf("dimensions = %s, want [[\"EventId\"],[]]", dims)
	}
	if unit := cw["Metrics"].([]any)[0].(map[string]any)["Unit"]; unit != "Count" {
		t.Errorf("unit = %v", unit)
	}
}

func TestAnEventGaugeKeepsTheUnitRuleOfRecord(t *testing.T) {
	var buf bytes.Buffer
	m := New("Rusuh/Admission", &buf)
	m.RecordEvent("QueueDepth", "evt-123", 800)
	m.RecordEvent("CtlHeartbeatAgeSeconds", "evt-123", 4.5)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	for i, want := range []struct {
		name, unit string
		value      float64
	}{{"QueueDepth", "None", 800}, {"CtlHeartbeatAgeSeconds", "Seconds", 4.5}} {
		got, cw := decodeLine(t, lines[i])
		if got[want.name] != want.value || got["EventId"] != "evt-123" {
			t.Errorf("line %d fields = %v", i, got)
		}
		if unit := cw["Metrics"].([]any)[0].(map[string]any)["Unit"]; unit != want.unit {
			t.Errorf("%s unit = %v, want %s", want.name, unit, want.unit)
		}
	}
}

func TestTwoEventsGiveTwoSeparateValues(t *testing.T) {
	var buf bytes.Buffer
	m := New("Rusuh/Admission", &buf)
	m.RecordEvent("QueueDepth", "evt-a", 800)
	m.RecordEvent("QueueDepth", "evt-b", 50)

	byEvent := map[string]float64{}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		got, _ := decodeLine(t, line)
		byEvent[got["EventId"].(string)] = got["QueueDepth"].(float64)
	}
	if byEvent["evt-a"] != 800 || byEvent["evt-b"] != 50 {
		t.Errorf("queue depth by event = %v", byEvent)
	}
}

func TestAMetricWithoutAnEventHasNoDimension(t *testing.T) {
	var buf bytes.Buffer
	m := New("Rusuh/Test", &buf)
	m.Incr("TickOverrun")
	m.IncrEvent("QueueJoined", "") // an empty id must not become an empty dimension value

	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		got, cw := decodeLine(t, line)
		if _, ok := got["EventId"]; ok {
			t.Errorf("unexpected EventId in %v", got)
		}
		dims, _ := json.Marshal(cw["Dimensions"])
		if string(dims) != `[[]]` {
			t.Errorf("dimensions = %s, want [[]]", dims)
		}
	}
}
