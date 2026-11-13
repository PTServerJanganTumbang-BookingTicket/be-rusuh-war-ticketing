package metrics

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestEMF_linesAreCloudWatchMetrics(t *testing.T) {
	var out bytes.Buffer
	e := NewEMF(&out)
	e.now = func() time.Time { return time.UnixMilli(1700000000000) }
	e.Incr("SSEBadMessage")
	e.Gauge("CtlHeartbeatAge", "E1", 12)
	e.Gauge("SSEResubscribeDelaySeconds", "", 1.5)
	e.Gauge("SSEConnections", "E1", 3)

	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	if len(lines) != 4 {
		t.Fatalf("%d lines, want 4: %s", len(lines), out.String())
	}
	unit := func(line []byte) any {
		var l map[string]any
		_ = json.Unmarshal(line, &l)
		return l["_aws"].(map[string]any)["CloudWatchMetrics"].([]any)[0].(map[string]any)["Metrics"].([]any)[0].(map[string]any)["Unit"]
	}
	if unit(lines[1]) != "Seconds" || unit(lines[3]) != "None" {
		t.Errorf("units: heartbeat age %v, connections %v", unit(lines[1]), unit(lines[3]))
	}
	var m map[string]any
	if err := json.Unmarshal(lines[1], &m); err != nil {
		t.Fatal(err)
	}
	if m["CtlHeartbeatAge"] != 12.0 || m["EventId"] != "E1" {
		t.Errorf("gauge line = %v", m)
	}
	cw := m["_aws"].(map[string]any)["CloudWatchMetrics"].([]any)[0].(map[string]any)
	if cw["Namespace"] != Namespace || cw["Dimensions"].([]any)[0].([]any)[0] != "EventId" || len(cw["Dimensions"].([]any)) != 2 {
		t.Errorf("CloudWatchMetrics = %v", cw)
	}
	if err := json.Unmarshal(lines[2], &m); err != nil || m["_aws"].(map[string]any)["CloudWatchMetrics"].([]any)[0].(map[string]any)["Metrics"].([]any)[0].(map[string]any)["Unit"] != "Seconds" {
		t.Errorf("a name ending in Seconds uses the Seconds unit: %v %v", m, err)
	}
}
