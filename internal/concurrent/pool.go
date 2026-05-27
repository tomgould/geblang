// Package concurrent provides a bounded-concurrency pool used by
// http.listen / http.serve / net.serve to cap simultaneous in-flight
// handlers and apply backpressure on overload.
package concurrent

import (
	"context"
	"errors"
	"sync/atomic"
)

// ErrOverloaded is returned by Acquire when the pool is full and
// the overload policy is Reject or Drop. Callers translate this
// into 503 for HTTP or a clean close for raw TCP.
var ErrOverloaded = errors.New("pool overloaded")

// OverloadPolicy controls Acquire's behaviour when both the slot
// set and the queue are full.
type OverloadPolicy int

const (
	// Reject returns ErrOverloaded immediately when no slot or queue
	// room is available.
	Reject OverloadPolicy = iota
	// Wait blocks the caller until a slot opens, regardless of
	// queue size. Effectively unbounded backpressure.
	Wait
	// Drop returns ErrOverloaded like Reject but signals to the
	// caller that the connection should be closed silently rather
	// than served a body.
	Drop
)

// Stats is a snapshot of pool counters. All fields are monotonic
// (active goes up and down; rejected only goes up).
type Stats struct {
	Active        int64
	Queued        int64
	Rejected      int64
	MaxConcurrent int64
}

// Pool gates Acquire calls to at most MaxConcurrent at once, with
// an optional queue for callers waiting on a slot. Zero
// MaxConcurrent disables all gating (unbounded mode).
type Pool struct {
	slots    chan struct{}
	queue    chan struct{}
	policy   OverloadPolicy
	max      int64
	active   atomic.Int64
	queued   atomic.Int64
	rejected atomic.Int64
}

// NewPool builds a pool. maxConcurrent == 0 disables gating;
// queueSize can be 0 (no queue, every overflow is rejected
// immediately). Negative values are treated as zero.
func NewPool(maxConcurrent, queueSize int, policy OverloadPolicy) *Pool {
	if maxConcurrent <= 0 {
		return &Pool{policy: policy}
	}
	if queueSize < 0 {
		queueSize = 0
	}
	p := &Pool{
		slots:  make(chan struct{}, maxConcurrent),
		policy: policy,
		max:    int64(maxConcurrent),
	}
	if queueSize > 0 {
		p.queue = make(chan struct{}, queueSize)
	}
	return p
}

// IsUnbounded reports whether this pool gates anything.
func (p *Pool) IsUnbounded() bool {
	return p == nil || p.slots == nil
}

// Acquire reserves a slot or returns ErrOverloaded per the policy.
// Callers MUST call Release when their work finishes (idiomatically
// via defer). ctx cancellation interrupts a Wait.
func (p *Pool) Acquire(ctx context.Context) error {
	if p.IsUnbounded() {
		return nil
	}
	select {
	case p.slots <- struct{}{}:
		p.active.Add(1)
		return nil
	default:
	}
	if p.policy == Wait {
		select {
		case p.slots <- struct{}{}:
			p.active.Add(1)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if p.queue == nil {
		p.rejected.Add(1)
		return ErrOverloaded
	}
	select {
	case p.queue <- struct{}{}:
	default:
		p.rejected.Add(1)
		return ErrOverloaded
	}
	p.queued.Add(1)
	defer func() {
		<-p.queue
		p.queued.Add(-1)
	}()
	select {
	case p.slots <- struct{}{}:
		p.active.Add(1)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release returns a slot to the pool. Unbounded pools no-op.
func (p *Pool) Release() {
	if p.IsUnbounded() {
		return
	}
	select {
	case <-p.slots:
		p.active.Add(-1)
	default:
	}
}

// Stats reads the pool's current counters atomically.
func (p *Pool) Stats() Stats {
	if p == nil {
		return Stats{}
	}
	return Stats{
		Active:        p.active.Load(),
		Queued:        p.queued.Load(),
		Rejected:      p.rejected.Load(),
		MaxConcurrent: p.max,
	}
}

// Policy returns the configured overload policy.
func (p *Pool) Policy() OverloadPolicy {
	if p == nil {
		return Reject
	}
	return p.policy
}

// ParsePolicy converts the user-facing string ("reject" / "wait" /
// "drop") to an OverloadPolicy. Unknown strings fall back to Reject.
func ParsePolicy(s string) OverloadPolicy {
	switch s {
	case "wait":
		return Wait
	case "drop":
		return Drop
	}
	return Reject
}
