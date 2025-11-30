package control

import (
	"sync/atomic"
	"time"
)

// SchedulerMetrics captures live scheduler telemetry in a goroutine-safe manner.
type SchedulerMetrics struct {
	queues             map[string]*atomicInt64
	holdingCurrent     atomicInt64
	holdingTotal       atomicInt64
	arrivals           atomicInt64
	totalWaitMicros    atomicInt64
	landings           atomicInt64
	totalLandingMicros atomicInt64
	conflicts          atomicInt64
}

// MetricsSnapshot is a read-only view of the current metrics.
type MetricsSnapshot struct {
	TotalArrivals      int64            `json:"totalArrivals"`
	AverageWaitSeconds float64          `json:"averageWaitSeconds"`
	AverageLandingTime float64          `json:"averageLandingSeconds"`
	HoldingCurrent     int64            `json:"holdingCurrent"`
	HoldingPatterns    int64            `json:"holdingPatterns"`
	QueueLengths       map[string]int64 `json:"queueLengths"`
	ConflictDetections int64            `json:"conflicts"`
}

// NewSchedulerMetrics builds a metrics collector for the supplied runway names.
func NewSchedulerMetrics(runways []string) *SchedulerMetrics {
	queues := make(map[string]*atomicInt64, len(runways))
	for _, r := range runways {
		queues[r] = &atomicInt64{}
	}
	return &SchedulerMetrics{queues: queues}
}

// RecordAssignment registers an arrival assigned to a runway.
func (m *SchedulerMetrics) RecordAssignment(wait time.Duration) {
	m.arrivals.Add(1)
	m.totalWaitMicros.Add(wait.Microseconds())
}

// RecordLanding captures the duration from assignment to touchdown.
func (m *SchedulerMetrics) RecordLanding(duration time.Duration) {
	m.landings.Add(1)
	m.totalLandingMicros.Add(duration.Microseconds())
}

// RecordHoldingPattern increments the count of flights sent to holding.
func (m *SchedulerMetrics) RecordHoldingPattern() {
	m.holdingTotal.Add(1)
}

// RecordConflict increments the conflict counter.
func (m *SchedulerMetrics) RecordConflict() {
	m.conflicts.Add(1)
}

// SetHolding updates the current number of flights in holding.
func (m *SchedulerMetrics) SetHolding(count int) {
	m.holdingCurrent.Store(int64(count))
}

// UpdateQueueLength stores the current queue length for a runway.
func (m *SchedulerMetrics) UpdateQueueLength(runway string, count int) {
	gauge, ok := m.queues[runway]
	if !ok {
		return
	}
	gauge.Store(int64(count))
}

// Snapshot returns a copy of the current metrics values.
func (m *SchedulerMetrics) Snapshot() MetricsSnapshot {
	queues := m.readQueueLengths()

	arrivals := m.arrivals.Load()
	waitAvg := 0.0
	if arrivals > 0 {
		waitAvg = float64(m.totalWaitMicros.Load()) / float64(arrivals) / 1_000_000
	}

	landings := m.landings.Load()
	landingAvg := 0.0
	if landings > 0 {
		landingAvg = float64(m.totalLandingMicros.Load()) / float64(landings) / 1_000_000
	}

	return MetricsSnapshot{
		TotalArrivals:      arrivals,
		AverageWaitSeconds: waitAvg,
		AverageLandingTime: landingAvg,
		HoldingCurrent:     m.holdingCurrent.Load(),
		HoldingPatterns:    m.holdingTotal.Load(),
		QueueLengths:       queues,
		ConflictDetections: m.conflicts.Load(),
	}
}

func (m *SchedulerMetrics) readQueueLengths() map[string]int64 {
	out := make(map[string]int64, len(m.queues))
	for runway, gauge := range m.queues {
		out[runway] = gauge.Load()
	}
	return out
}

// atomicInt64 is a tiny wrapper to avoid importing sync/atomic in the UI layer.
type atomicInt64 struct {
	val atomic.Int64
}

func (a *atomicInt64) Add(v int64) int64 { return a.val.Add(v) }
func (a *atomicInt64) Load() int64       { return a.val.Load() }
func (a *atomicInt64) Store(v int64)     { a.val.Store(v) }
