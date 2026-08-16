package background

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeChecker struct {
	calls int32
	err   error
}

func (f *fakeChecker) CheckDeadlines(ctx context.Context) error {
	atomic.AddInt32(&f.calls, 1)
	return f.err
}

func TestMonitor_RunsAndCancels(t *testing.T) {
	fc := &fakeChecker{}
	m := NewMonitor(fc, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()

	time.Sleep(40 * time.Millisecond)
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if atomic.LoadInt32(&fc.calls) < 1 {
		t.Fatal("expected at least one check call")
	}
	stats := m.Stats()
	if stats.Ticks < 1 {
		t.Fatalf("expected at least one tick, got %d", stats.Ticks)
	}
	if stats.LastRun.IsZero() {
		t.Fatal("expected non-zero last run")
	}
}

func TestMonitor_ErrorCounting(t *testing.T) {
	fc := &fakeChecker{err: errors.New("boom")}
	m := NewMonitor(fc, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()

	time.Sleep(40 * time.Millisecond)
	cancel()
	<-done

	if m.Stats().Errors < 1 {
		t.Fatalf("expected at least one error, got %d", m.Stats().Errors)
	}
	if m.Stats().Ticks != 0 {
		t.Fatalf("successful ticks should be 0 when all checks error, got %d", m.Stats().Ticks)
	}
}

func TestMonitor_DefaultInterval(t *testing.T) {
	m := NewMonitor(&fakeChecker{}, 0)
	if m.interval != 5*time.Second {
		t.Fatalf("expected default 5s, got %s", m.interval)
	}
}
