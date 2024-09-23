// Copyright 2024 Matthew P. Dargan. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package rate provides a rate limiter.
package rate

import (
	"context"
	"sync/atomic"
	"time"
)

// A Limiter controls how frequently events are allowed to happen. It
// implements a [GCRA] (Generic Cell Rate Algorithm) leaky bucket variant.
//
// The zero value is a valid Limiter that allows all requests. Use [NewLimiter]
// to create non-zero Limiters.
//
// Limiter is safe for simultaneous use by multiple goroutines.
//
// [GCRA]: https://en.wikipedia.org/wiki/Generic_cell_rate_algorithm
type Limiter struct {
	interval time.Duration // emission interval
	tau      time.Duration // delay variation tolerance
	tat      atomic.Int64  // theoretical arrival time
}

// NewLimiter returns a new Limiter that allows events up to rate r and
// permits bursts of at most b events.
//
// The rate r is specified in events per second. If r <= 0, the Limiter
// allows all events.
func NewLimiter(r float64, b uint) *Limiter {
	var i time.Duration
	if r > 0 {
		i = time.Duration(float64(time.Second) / r)
	}
	l := &Limiter{
		interval: i,
		tau:      i * time.Duration(max(1, b)),
	}
	l.tat.Store(time.Now().UnixNano())
	return l
}

// Allow reports whether an event may happen now.
func (l *Limiter) Allow() bool {
	return l.AllowN(time.Now(), 1)
}

// AllowN reports whether n events may happen at time t. Use this method if
// you intend to drop/skip events that exceed the rate limit.
func (l *Limiter) AllowN(t time.Time, n uint) bool {
	if l.interval == 0 || n == 0 {
		return true
	}
	for {
		tat := l.tat.Load()
		next := l.nextTat(t, tat, n)
		if t.Before(next.Add(-l.tau)) {
			return false
		}
		if l.tat.CompareAndSwap(tat, next.UnixNano()) {
			return true
		}
	}
}

// Wait blocks until l permits an event to happen.
func (l *Limiter) Wait(ctx context.Context) error {
	return l.WaitN(ctx, time.Now(), 1)
}

// WaitN blocks until l permits n events to happen at time t. It returns an
// error if the Context is canceled or the Context's deadline expired.
func (l *Limiter) WaitN(ctx context.Context, t time.Time, n uint) error {
	if l.interval == 0 || n == 0 {
		return nil
	}
	var d time.Duration
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tat := l.tat.Load()
		next := l.nextTat(t, tat, n)
		allowAt := next.Add(-l.tau)
		if t.Before(allowAt) {
			d = allowAt.Sub(t)
			break
		}
		if l.tat.CompareAndSwap(tat, next.UnixNano()) {
			return nil
		}
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Limiter) nextTat(t time.Time, tat int64, n uint) time.Time {
	nextTat := time.Unix(0, tat)
	i := time.Duration(n) * l.interval
	if t.After(nextTat) {
		return t.Add(i)
	}
	return nextTat.Add(i)
}
