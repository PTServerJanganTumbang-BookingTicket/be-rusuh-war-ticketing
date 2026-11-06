// Package metrics writes CloudWatch metrics as Embedded Metric Format log
// lines: no SDK call on the hot path, CloudWatch extracts them from the logs.
package metrics

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Namespace is the CloudWatch namespace for queue-service metrics.
const Namespace = "Rusuh/Queue"

// EMF writes one JSON line per data point.
type EMF struct {
	mu  sync.Mutex
	out io.Writer
	now func() time.Time
}

// NewEMF writes to out (os.Stdout in Lambda).
func NewEMF(out io.Writer) *EMF { return &EMF{out: out, now: time.Now} }

// Incr adds 1 to a count metric.
func (e *EMF) Incr(name string) { e.write(name, 1, "Count") }

// Record records a value; names ending in "Seconds" use the Seconds unit.
func (e *EMF) Record(name string, value float64) {
	unit := "None"
	if len(name) > 7 && name[len(name)-7:] == "Seconds" {
		unit = "Seconds"
	}
	e.write(name, value, unit)
}

func (e *EMF) write(name string, value float64, unit string) {
	line := map[string]any{
		"_aws": map[string]any{
			"Timestamp": e.now().UnixMilli(),
			"CloudWatchMetrics": []any{map[string]any{
				"Namespace":  Namespace,
				"Dimensions": [][]string{{}},
				"Metrics":    []any{map[string]string{"Name": name, "Unit": unit}},
			}},
		},
		name: value,
	}
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.out.Write(append(b, '\n'))
}
