package control

import (
	"context"
	"log"
	"math"
	"sync"
	"time"
)

const minArrivalSpacing = 2 * time.Second

// RunwayManager tracks runway availability and assigns inbound flights.
type RunwayManager struct {
	mu       sync.Mutex
	runways  map[string]*runwayState
	assigned map[string][]Flight
	vectors  map[int64]float64
	holding  []Flight
	order    []string
	nextIdx  int
	wind     WindState
	metrics  *SchedulerMetrics
	lastUse  map[string]time.Time
}

// WindState captures the current wind speed (knots) and direction (degrees true).
type WindState struct {
	Speed     int64 `json:"speed"`
	Direction int64 `json:"direction"`
}

// RunwayDefinition describes the reference heading for a runway's primary threshold.
type RunwayDefinition struct {
	Name    string
	Heading float64
}

type runwayState struct {
	definition    RunwayDefinition
	open          bool
	activeHeading float64
}

// NewRunwayManager constructs a RunwayManager for the supplied runway names.
func NewRunwayManager(runways []RunwayDefinition, metrics *SchedulerMetrics) *RunwayManager {
	rm := &RunwayManager{
		runways:  make(map[string]*runwayState, len(runways)),
		assigned: make(map[string][]Flight, len(runways)),
		vectors:  make(map[int64]float64),
		order:    make([]string, 0, len(runways)),
		wind:     WindState{Speed: 0, Direction: 0},
		lastUse:  make(map[string]time.Time, len(runways)),
		metrics:  metrics,
	}
	for _, r := range runways {
		rm.runways[r.Name] = &runwayState{definition: r, open: true, activeHeading: normalizeHeading(r.Heading)}
		rm.order = append(rm.order, r.Name)
	}
	rm.updateActiveHeadingsLocked()
	return rm
}

// Run consumes flight arrivals and assigns them to available runways using
// round-robin sequencing. Flights are diverted to holding if no runways are
// available.
func (rm *RunwayManager) Run(ctx context.Context, flights <-chan Flight) {
	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-flights:
			if !ok {
				return
			}
			log.Printf("spawned flight %d (%s)", f.ID, f.Call)
			rm.AssignFlight(f)
		}
	}
}

// AssignFlight assigns a flight to the next available runway, or to holding
// if none are available.
func (rm *RunwayManager) AssignFlight(f Flight) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.updateActiveHeadingsLocked()
	runway := rm.nextRunway()
	if runway == "" {
		rm.holding = append(rm.holding, f)
		rm.recordHoldingLocked(1)
		rm.publishHoldingLocked()
		log.Printf("flight %d (%s) holding: no runway available", f.ID, f.Call)
		return
	}

	rm.assigned[runway] = append(rm.assigned[runway], f)
	targetHeading := rm.runways[runway].activeHeading
	rm.vectors[f.ID] = rm.smoothVector(rm.vectors[f.ID], targetHeading)
	rm.recordAssignmentLocked(time.Since(f.CreatedAt))
	rm.detectConflictLocked(runway)
	rm.lastUse[runway] = time.Now()
	rm.publishQueuesLocked(runway)
	log.Printf("flight %d (%s) assigned to %s on heading %.0f°", f.ID, f.Call, runway, rm.vectors[f.ID])

	assignedAt := time.Now()
	go rm.completeLanding(runway, f, assignedAt)
}

// SetRunwayClosed updates the runway state and handles diversion logic.
func (rm *RunwayManager) SetRunwayClosed(runway string, closed bool) {
	rm.mu.Lock()
	r, ok := rm.runways[runway]
	if !ok {
		// Unknown runway, nothing to do.
		rm.mu.Unlock()
		log.Printf("runway command ignored: unknown runway %s", runway)
		return
	}

	if closed {
		if !r.open {
			rm.mu.Unlock()
			return
		}
		r.open = false
		diverted := rm.assigned[runway]
		if len(diverted) > 0 {
			rm.holding = append(rm.holding, diverted...)
			rm.assigned[runway] = nil
			rm.publishQueuesLocked(runway)
			rm.recordHoldingLocked(len(diverted))
			rm.publishHoldingLocked()
			log.Printf("runway %s closed; diverted %d flights to holding", runway, len(diverted))
		} else {
			log.Printf("runway %s closed", runway)
		}
		rm.mu.Unlock()
		return
	}

	if r.open {
		rm.mu.Unlock()
		return
	}

	r.open = true
	holding := rm.holding
	rm.holding = nil
	rm.publishHoldingLocked()
	rm.mu.Unlock()

	log.Printf("runway %s reopened; reassigning %d holding flights", runway, len(holding))
	for _, f := range holding {
		rm.AssignFlight(f)
	}
}

// IsClosed returns true when the runway is currently closed.
func (rm *RunwayManager) IsClosed(runway string) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	state, ok := rm.runways[runway]
	if !ok {
		return false
	}
	return !state.open
}

// RunwayNames returns the known runway identifiers in scheduling order.
func (rm *RunwayManager) RunwayNames() []string {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	names := make([]string, len(rm.order))
	copy(names, rm.order)
	return names
}

func (rm *RunwayManager) nextRunway() string {
	open := rm.openRunways()
	if len(open) == 0 {
		return ""
	}
	runway := open[rm.nextIdx%len(open)]
	rm.nextIdx++
	return runway
}

func (rm *RunwayManager) openRunways() []string {
	open := make([]string, 0, len(rm.order))
	for _, name := range rm.order {
		if rm.runways[name].open {
			open = append(open, name)
		}
	}
	return open
}

// SetWind updates the active wind state and re-vectors existing assignments to
// fly toward the new into-wind threshold.
func (rm *RunwayManager) SetWind(speed, direction int64) {
	rm.mu.Lock()
	rm.wind = WindState{Speed: maxInt64(speed, 0), Direction: normalizeDirection(direction)}
	rm.updateActiveHeadingsLocked()
	rm.revectorLocked()
	rm.mu.Unlock()
}

// Wind returns the current wind state.
func (rm *RunwayManager) Wind() WindState {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	return rm.wind
}

func (rm *RunwayManager) updateActiveHeadingsLocked() {
	for _, r := range rm.runways {
		r.activeHeading = rm.bestHeading(r.definition)
	}
}

func (rm *RunwayManager) bestHeading(def RunwayDefinition) float64 {
	base := normalizeHeading(def.Heading)
	reciprocal := normalizeHeading(def.Heading + 180)

	if rm.wind.Speed == 0 {
		return base
	}

	windDir := float64(rm.wind.Direction)
	if angularDiff(windDir, base) <= angularDiff(windDir, reciprocal) {
		return base
	}
	return reciprocal
}

func (rm *RunwayManager) revectorLocked() {
	for runway, flights := range rm.assigned {
		target := rm.runways[runway].activeHeading
		for _, f := range flights {
			prev := rm.vectors[f.ID]
			next := rm.smoothVector(prev, target)
			rm.vectors[f.ID] = next
			if prev != next {
				log.Printf("flight %d (%s) re-vectored toward heading %.0f° for runway %s", f.ID, f.Call, next, runway)
			}
		}
	}
}

func (rm *RunwayManager) smoothVector(current, target float64) float64 {
	if current == 0 {
		return target
	}

	const maxChange = 35.0
	delta := signedAngularDiff(current, target)
	if math.Abs(delta) > maxChange {
		delta = math.Copysign(maxChange, delta)
	}
	return normalizeHeading(current + delta)
}

func (rm *RunwayManager) recordAssignmentLocked(wait time.Duration) {
	if rm.metrics == nil {
		return
	}
	rm.metrics.RecordAssignment(wait)
}

func (rm *RunwayManager) recordHoldingLocked(count int) {
	if rm.metrics == nil {
		return
	}
	for i := 0; i < count; i++ {
		rm.metrics.RecordHoldingPattern()
	}
}

func (rm *RunwayManager) publishHoldingLocked() {
	if rm.metrics == nil {
		return
	}
	rm.metrics.SetHolding(len(rm.holding))
}

func (rm *RunwayManager) publishQueuesLocked(runway string) {
	if rm.metrics == nil {
		return
	}
	rm.metrics.UpdateQueueLength(runway, len(rm.assigned[runway]))
}

func (rm *RunwayManager) detectConflictLocked(runway string) {
	last, ok := rm.lastUse[runway]
	if !ok {
		return
	}
	delta := time.Since(last)
	if delta < minArrivalSpacing {
		if rm.metrics != nil {
			rm.metrics.RecordConflict()
		}
		log.Printf("spacing conflict detected on %s (%.1fs apart)", runway, delta.Seconds())
	}
}

func (rm *RunwayManager) completeLanding(runway string, f Flight, assignedAt time.Time) {
	const landingDuration = 5 * time.Second
	time.Sleep(landingDuration)

	rm.mu.Lock()
	queue := rm.assigned[runway]
	for i, candidate := range queue {
		if candidate.ID == f.ID {
			queue = append(queue[:i], queue[i+1:]...)
			break
		}
	}
	rm.assigned[runway] = queue
	rm.publishQueuesLocked(runway)
	rm.mu.Unlock()

	if rm.metrics != nil {
		rm.metrics.RecordLanding(time.Since(assignedAt))
	}
}

func normalizeHeading(deg float64) float64 {
	deg = math.Mod(deg, 360)
	if deg < 0 {
		deg += 360
	}
	return deg
}

func normalizeDirection(dir int64) int64 {
	d := dir % 360
	if d < 0 {
		d += 360
	}
	return d
}

func angularDiff(a, b float64) float64 {
	return math.Abs(signedAngularDiff(a, b))
}

func signedAngularDiff(from, to float64) float64 {
	diff := math.Mod(to-from+540, 360) - 180
	if diff == -180 {
		return 180
	}
	return diff
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
