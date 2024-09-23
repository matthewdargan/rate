// Copyright 2024 Matthew P. Dargan. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rate_test

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matthewdargan/rate"
)

const d = 100 * time.Millisecond

var (
	t0 = time.Now()
	t1 = t0.Add(d)
	t2 = t0.Add(time.Duration(2) * d)
	t3 = t0.Add(time.Duration(3) * d)
	t4 = t0.Add(time.Duration(4) * d)
	t9 = t0.Add(time.Duration(9) * d)
)

type allow struct {
	t  time.Time
	n  uint
	ok bool
}

func run(t *testing.T, l *rate.Limiter, allows []allow) {
	t.Helper()
	for i, a := range allows {
		if ok := l.AllowN(a.t, a.n); ok != a.ok {
			t.Errorf("step %d: l.AllowN(%v, %d) = %v want %v", i, a.t, a.n, ok, a.ok)
		}
	}
}

func TestLimiterBurst1(t *testing.T) {
	t.Parallel()
	run(t, rate.NewLimiter(10, 1), []allow{
		{t0, 1, false},
		{t0, 1, false},
		{t0, 1, false},
		{t1, 1, true},
		{t1, 1, false},
		{t1, 1, false},
		{t2, 2, false},
		{t2, 1, true},
		{t2, 1, false},
	})
}

func TestLimiterBurst3(t *testing.T) {
	t.Parallel()
	run(t, rate.NewLimiter(10, 3), []allow{
		{t0, 2, true},
		{t0, 2, false},
		{t0, 1, false},
		{t0, 1, false},
		{t1, 4, false},
		{t2, 1, true},
		{t3, 1, true},
		{t4, 1, true},
		{t4, 1, true},
		{t4, 1, false},
		{t4, 1, false},
		{t9, 3, true},
		{t9, 0, true},
	})
}

func TestLimiterJumpBackwards(t *testing.T) {
	t.Parallel()
	run(t, rate.NewLimiter(10, 3), []allow{
		{t1, 1, true}, // start at t1
		{t0, 1, true}, // jump back to t0
		{t0, 1, false},
		{t0, 1, false},
		{t0, 1, false},
		{t1, 1, true}, // got an event
		{t1, 1, false},
		{t1, 1, false},
		{t2, 1, true}, // got another event
		{t2, 1, false},
		{t2, 1, false},
	})
}

func TestSimultaneousRequests(t *testing.T) {
	t.Parallel()
	const (
		burst = 5
		n     = 15
	)
	var (
		wg    sync.WaitGroup
		numOK atomic.Uint32
	)
	lim := rate.NewLimiter(1, burst)
	f := func() {
		defer wg.Done()
		if ok := lim.Allow(); ok {
			numOK.Add(1)
		}
	}
	wg.Add(n)
	for range n {
		go f()
	}
	wg.Wait()
	nOK := numOK.Load()
	if nOK != burst {
		t.Errorf("numOK = %d, want %d", nOK, burst)
	}
}

func TestZeroLimit(t *testing.T) {
	t.Parallel()
	var l rate.Limiter
	if !l.Allow() {
		t.Error("lim.Allow() = false, want true")
	}
	if !l.AllowN(time.Now(), math.MaxUint32) {
		t.Error("lim.AllowN(time.Now(), math.MaxUint32) = false, want true")
	}
}
