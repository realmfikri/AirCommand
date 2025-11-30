package control

import (
	"context"
	"log"
	"sync"
)

// RunwayManager tracks runway availability and assigns inbound flights.
type RunwayManager struct {
	mu       sync.Mutex
	runways  map[string]*runwayState
	assigned map[string][]Flight
	holding  []Flight
	order    []string
	nextIdx  int
}

type runwayState struct {
	open bool
}

// NewRunwayManager constructs a RunwayManager for the supplied runway names.
func NewRunwayManager(runways []string) *RunwayManager {
	rm := &RunwayManager{
		runways:  make(map[string]*runwayState, len(runways)),
		assigned: make(map[string][]Flight, len(runways)),
		order:    make([]string, 0, len(runways)),
	}
	for _, r := range runways {
		rm.runways[r] = &runwayState{open: true}
		rm.order = append(rm.order, r)
	}
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

	runway := rm.nextRunway()
	if runway == "" {
		rm.holding = append(rm.holding, f)
		log.Printf("flight %d (%s) holding: no runway available", f.ID, f.Call)
		return
	}

	rm.assigned[runway] = append(rm.assigned[runway], f)
	log.Printf("flight %d (%s) assigned to %s", f.ID, f.Call, runway)
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
