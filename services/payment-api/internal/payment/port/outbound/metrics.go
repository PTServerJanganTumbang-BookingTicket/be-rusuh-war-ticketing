package outbound

// IMetrics counts payment events as CloudWatch metrics (docs/metrics.md). IncrEvent keeps the count per event and in
// the all-events total; Incr is for counts that are not about one event.
type IMetrics interface {
	Incr(name string)
	IncrEvent(name, eventID string)
}
