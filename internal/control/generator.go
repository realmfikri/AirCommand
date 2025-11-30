package control

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

// Generator simulates flight arrivals at a configurable rate.
type Generator struct {
	ratePerMinute atomic.Int64
	nextID        atomic.Int64
}

// NewGenerator constructs a generator with a default rate.
func NewGenerator(defaultRate int64) *Generator {
	g := &Generator{}
	if defaultRate <= 0 {
		defaultRate = 1
	}
	g.ratePerMinute.Store(defaultRate)
	return g
}

// SetRate updates the generator rate in planes per minute.
func (g *Generator) SetRate(rate int64) {
	if rate <= 0 {
		rate = 1
	}
	g.ratePerMinute.Store(rate)
	log.Printf("arrival rate updated: %d planes/min", rate)
}

// Rate returns the current rate in planes per minute.
func (g *Generator) Rate() int64 {
	rate := g.ratePerMinute.Load()
	if rate <= 0 {
		return 1
	}
	return rate
}

// Flight represents a generated flight payload.
type Flight struct {
	ID   int64  `json:"id"`
	Call string `json:"call"`
}

// Run starts generating flights until the context is canceled.
func (g *Generator) Run(ctx context.Context, out chan<- Flight) {
	for {
		select {
		case <-ctx.Done():
			close(out)
			return
		case <-time.After(g.interval()):
			flight := g.spawn()
			select {
			case <-ctx.Done():
				close(out)
				return
			case out <- flight:
			}
		}
	}
}

func (g *Generator) interval() time.Duration {
	rate := g.Rate()
	return time.Minute / time.Duration(rate)
}

func (g *Generator) spawn() Flight {
	id := g.nextID.Add(1)
	return Flight{
		ID:   id,
		Call: "FLT" + time.Now().Format("150405") + "-" + fmt.Sprintf("%04d", id%10000),
	}
}
