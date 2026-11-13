// Package metrics writes admission-ctl's metrics as CloudWatch EMF lines (docs/metrics.md).
package metrics

import (
	"io"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/emf"
)

// Namespace is the CloudWatch namespace of admission-ctl.
const Namespace = "Rusuh/Admission"

// EMF implements outbound.Metrics over pkg/emf.
type EMF struct{ e *emf.EMF }

// NewEMF writes to w (os.Stdout on ECS; the awslogs driver ships it to CloudWatch).
func NewEMF(w io.Writer) *EMF { return &EMF{e: emf.New(Namespace, w)} }

// Inc adds 1 to a count with no event.
func (m *EMF) Inc(name string) { m.e.Incr(name) }

// Gauge records a value with no event.
func (m *EMF) Gauge(name string, value float64) { m.e.Record(name, value) }

// IncEvent adds 1 to a count of one event.
func (m *EMF) IncEvent(name, eventID string) { m.e.IncrEvent(name, eventID) }

// GaugeEvent records a value of one event.
func (m *EMF) GaugeEvent(name, eventID string, value float64) { m.e.RecordEvent(name, eventID, value) }
