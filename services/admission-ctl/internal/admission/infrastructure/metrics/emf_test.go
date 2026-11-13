package metrics

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestTheAdapterWritesAdmissionLinesWithTheEventWhenThereIsOne(t *testing.T) {
	var buf bytes.Buffer
	m := NewEMF(&buf)
	m.GaugeEvent("QueueDepth", "evt-a", 800)
	m.IncEvent("Granted", "evt-a")
	m.Inc("TickOverrun")
	m.Gauge("StockDrift", 2)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines: %q", len(lines), buf.String())
	}
	for i, wantEvent := range []bool{true, true, false, false} {
		var got map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &got); err != nil {
			t.Fatal(err)
		}
		cw := got["_aws"].(map[string]any)["CloudWatchMetrics"].([]any)[0].(map[string]any)
		if cw["Namespace"] != "Rusuh/Admission" {
			t.Errorf("line %d namespace = %v", i, cw["Namespace"])
		}
		if _, has := got["EventId"]; has != wantEvent {
			t.Errorf("line %d has EventId = %v, want %v", i, has, wantEvent)
		}
	}
}
