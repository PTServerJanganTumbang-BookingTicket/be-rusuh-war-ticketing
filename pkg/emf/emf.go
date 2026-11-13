// Package emf writes CloudWatch metrics as Embedded Metric Format log
// lines: no SDK call on the hot path, CloudWatch extracts them from the logs.
package emf

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// EMF writes one JSON line per data point.
type EMF struct {
	namespace string
	mu        sync.Mutex
	out       io.Writer
	now       func() time.Time
}

// New writes to out (os.Stdout in Lambda) under one CloudWatch namespace (Rusuh/Queue, Rusuh/Booking, ...).
func New(namespace string, out io.Writer) *EMF {
	return &EMF{namespace: namespace, out: out, now: time.Now}
}

// DimEventID is the one dimension per-event metrics carry (docs/metrics.md §3).
const DimEventID = "EventId"

// Incr adds 1 to a count metric.
func (e *EMF) Incr(name string) { e.write(name, "", 1, "Count") }

// Record records a value; names ending in "Seconds" use the Seconds unit.
func (e *EMF) Record(name string, value float64) { e.write(name, "", value, unitFor(name)) }

// IncrEvent adds 1 to a count metric of one event. The line feeds both the
// per-event metric and the all-events total.
func (e *EMF) IncrEvent(name, eventID string) { e.write(name, eventID, 1, "Count") }

// RecordEvent records a value of one event, with the unit rule of Record.
func (e *EMF) RecordEvent(name, eventID string, value float64) {
	e.write(name, eventID, value, unitFor(name))
}

func unitFor(name string) string {
	if len(name) > 7 && name[len(name)-7:] == "Seconds" {
		return "Seconds"
	}
	return "None"
}

// write emits one point. With an event id the line declares two dimension
// sets, [EventId] and [], so the same point counts per event and in the total.
func (e *EMF) write(name, eventID string, value float64, unit string) {
	dims := [][]string{{}}
	line := map[string]any{name: value}
	if eventID != "" {
		dims = [][]string{{DimEventID}, {}}
		line[DimEventID] = eventID
	}
	line["_aws"] = map[string]any{
		"Timestamp": e.now().UnixMilli(),
		"CloudWatchMetrics": []any{map[string]any{
			"Namespace":  e.namespace,
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
