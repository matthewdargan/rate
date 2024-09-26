// Copyright 2024 Matthew P. Dargan. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rate_test

import (
	"context"
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
	n  int
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
	l := rate.NewLimiter(1, burst)
	f := func() {
		defer wg.Done()
		if ok := l.Allow(); ok {
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

type testTime struct {
	mu  sync.Mutex
	cur time.Time
}

func (tt *testTime) now() time.Time {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	return tt.cur
}

func (tt *testTime) since(t time.Time) time.Duration {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	return tt.cur.Sub(t)
}

func (tt *testTime) advance(d time.Duration) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	tt.cur = tt.cur.Add(d)
}

func newTestTime() *testTime {
	return &testTime{cur: time.Now()}
}

func TestLongRunningQPS(t *testing.T) {
	// The test runs for a few (fake) seconds executing many requests
	// and checks that overall number of requests is reasonable.
	t.Parallel()
	const (
		limit = 100
		burst = 100
	)
	var (
		numOK atomic.Uint32
		tt    = newTestTime()
	)
	l := rate.NewLimiter(limit, burst)
	start := tt.now()
	end := start.Add(5 * time.Second)
	for tt.now().Before(end) {
		if ok := l.AllowN(tt.now(), 1); ok {
			numOK.Add(1)
		}
		// This will still offer ~500 requests per second, but won't consume
		// outrageous amount of CPU.
		tt.advance(2 * time.Millisecond)
	}
	elapsed := tt.since(start)
	ideal := burst + (limit * float64(elapsed) / float64(time.Second))
	// We should never get more requests than allowed.
	nOK := numOK.Load()
	if want := uint32(ideal + 1); nOK > want {
		t.Errorf("numOK = %d, want %d (ideal %f)", nOK, want, ideal)
	}
	// We should get very close to the number of requests allowed.
	if want := uint32(0.999 * ideal); nOK < want {
		t.Errorf("numOK = %d, want %d (ideal %f)", nOK, want, ideal)
	}
}

type wait struct {
	name   string
	ctx    context.Context
	n      int
	delay  int // in multiples of d
	nilErr bool
}

func runWait(t *testing.T, tt *testTime, l *rate.Limiter, w wait) {
	t.Helper()
	start := tt.now()
	err := l.WaitN(w.ctx, start, w.n)
	if w.n == 1 {
		err = l.Wait(w.ctx)
	}
	tt.mu.Lock()
	tt.cur = time.Now()
	tt.mu.Unlock()
	delay := tt.since(start)
	if (w.nilErr && err != nil) || (!w.nilErr && err == nil) || !waitDelayOk(w.delay, delay) {
		errString := "<nil>"
		if !w.nilErr {
			errString = "<non-nil error>"
		}
		t.Errorf("l.WaitN(%v, %v, %d) = %v with delay %v; want %v with delay %v (±%v)",
			w.name, start, w.n, err, delay, errString, d*time.Duration(w.delay), d/2)
	}
}

func waitDelayOk(wantD int, got time.Duration) bool {
	// nearest multiple of the global constant d
	gotD := int((got + (d / 2)) / d)
	// The actual time spent waiting will be REDUCED by the amount of time spent
	// since the last call to the limiter. We expect the time in between calls to
	// be executing simple, straight-line, non-blocking code, so it should reduce
	// the wait time by no more than half a d, which would round to exactly wantD.
	if gotD < wantD {
		return false
	}
	// The actual time spend waiting will be INCREASED by the amount of scheduling
	// slop in the platform's sleep syscall, plus the amount of time spent executing
	// straight-line code before measuring the elapsed duration.
	//
	// The latter is surely less than half a d, but the former is empirically
	// sometimes larger on a number of platforms for a number of reasons.
	// NetBSD and OpenBSD tend to overshoot sleeps by a wide margin due to a
	// suspected platform bug; see https://go.dev/issue/44067 and
	// https://go.dev/issue/50189.
	// Longer delays were also also observed on slower builders with Linux kernels
	// (linux-ppc64le-buildlet, android-amd64-emu), and on Solaris and Plan 9.
	//
	// Since d is already fairly generous, we take 150% of wantD rounded up —
	// that's at least enough to account for the overruns we've seen so far in
	// practice.
	maxD := (wantD*3 + 1) / 2
	return gotD <= maxD
}

func TestWaitSimple(t *testing.T) {
	t.Parallel()
	tt := newTestTime()
	l := rate.NewLimiter(10, 3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runWait(t, tt, l, wait{"already-cancelled", ctx, 1, 0, false})
	runWait(t, tt, l, wait{"act-now", context.Background(), 2, 0, true})
	runWait(t, tt, l, wait{"act-later", context.Background(), 3, 2, true})
}

func TestWaitCancel(t *testing.T) {
	t.Parallel()
	tt := newTestTime()
	l := rate.NewLimiter(10, 3)
	ctx, cancel := context.WithCancel(context.Background())
	runWait(t, tt, l, wait{"act-now", ctx, 2, 0, true})
	go func() {
		time.Sleep(d)
		tt.mu.Lock()
		tt.cur = time.Now()
		tt.mu.Unlock()
		cancel()
	}()
	runWait(t, tt, l, wait{"will-cancel", ctx, 3, 1, false})
	runWait(t, tt, l, wait{"act-now-after-cancel", context.Background(), 2, 0, true})
}

func TestWaitTimeout(t *testing.T) {
	t.Parallel()
	tt := newTestTime()
	l := rate.NewLimiter(10, 3)
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	runWait(t, tt, l, wait{"act-now", ctx, 2, 0, true})
	runWait(t, tt, l, wait{"w-timeout-err", ctx, 3, 1, false})
}

func TestWaitInf(t *testing.T) {
	t.Parallel()
	tt := newTestTime()
	l := rate.NewLimiter(math.MaxFloat64, 0)
	runWait(t, tt, l, wait{"exceed-burst-no-error", context.Background(), 3, 0, true})
}

func TestZeroLimit(t *testing.T) {
	t.Parallel()
	var l rate.Limiter
	if !l.Allow() {
		t.Error("l.Allow() = false, want true")
	}
	if !l.AllowN(time.Now(), math.MaxInt32) {
		t.Error("l.AllowN(time.Now(), math.MaxInt32) = false, want true")
	}
}

func BenchmarkAllowN(b *testing.B) {
	l := rate.NewLimiter(1, 1)
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.AllowN(now, 1)
		}
	})
}

func BenchmarkWaitNNoDelay(b *testing.B) {
	l := rate.NewLimiter(float64(b.N), b.N)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = l.WaitN(ctx, time.Now(), 1)
	}
}
