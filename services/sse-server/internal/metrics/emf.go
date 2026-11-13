// Package metrics writes CloudWatch metrics as Embedded Metric Format log
// lines: no SDK call on the hot path, CloudWatch extracts them from the logs.
package metrics

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Namespace is the CloudWatch namespace for sse-server metrics.
const Namespace = "Rusuh/SSE"

// EMF writes one JSON line per data point.
type EMF struct {
	mu  sync.Mutex
	out io.Writer
	now func() time.Time
}

// NewEMF writes to out (os.Stdout under ECS).
func NewEMF(out io.Writer) *EMF { return &EMF{out: out, now: time.Now} }

// Incr adds 1 to a count metric.
func (e *EMF) Incr(name string) { e.write(name, "", 1, "Count") }

// Gauge records a value for one event ("" for a server-wide value).
func (e *EMF) Gauge(name, event string, value float64) {
	unit := "None"
	if u, ok := units[name]; ok {
		unit = u
	}
	e.write(name, event, value, unit)
}

// units are the CloudWatch units of the gauges that are not plain counts.
var units = map[string]string{
	"CtlHeartbeatAge":            "Seconds",
	"SSEResubscribeDelaySeconds": "Seconds",
}

func (e *EMF) write(name, event string, value float64, unit string) {
	dims := [][]string{{}}
	line := map[string]any{name: value}
	if event != "" {
		// Per event and in the all-events total (docs/metrics.md §3): the heartbeat alarm watches the total.
		dims = [][]string{{"EventId"}, {}}
		line["EventId"] = event
	}
	line["_aws"] = map[string]any{
		"Timestamp": e.now().UnixMilli(),
		"CloudWatchMetrics": []any{map[string]any{
			"Namespace":  Namespace,
			"Dimensions": dims,
			"Metrics":    []any{map[string]string{"Name": name, "Unit": unit}},
		}},
	}
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.out.Write(append(b, '\n'))
}
