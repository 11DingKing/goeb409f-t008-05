package background

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// DeadlineChecker scans processes for expired deadlines (e.g. the diesel
// generator not started within its window). It is implemented by the
// application service.
type DeadlineChecker interface {
	CheckDeadlines(ctx context.Context) error
}

// Stats reports monitor execution counters.
type Stats struct {
	Ticks   int64
	Errors  int64
	LastRun time.Time
}

// Monitor periodically invokes a DeadlineChecker until its context is
// cancelled, providing the background watchdog for off-grid operation.
type Monitor struct {
	checker  DeadlineChecker
	interval time.Duration

	ticks   atomic.Int64
	errors  atomic.Int64
	lastRun atomic.Value
	stop    sync.Once
}

// NewMonitor creates a monitor running check at the given interval. A
// non-positive interval defaults to five seconds.
func NewMonitor(checker DeadlineChecker, interval time.Duration) *Monitor {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Monitor{checker: checker, interval: interval}
}

// Run blocks, invoking the checker on each tick, until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) error {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := m.checker.CheckDeadlines(ctx); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				m.errors.Add(1)
			} else {
				m.ticks.Add(1)
			}
			m.lastRun.Store(time.Now())
		}
	}
}

// Stats returns a snapshot of the monitor counters.
func (m *Monitor) Stats() Stats {
	var lr time.Time
	if v := m.lastRun.Load(); v != nil {
		lr = v.(time.Time)
	}
	return Stats{
		Ticks:   m.ticks.Load(),
		Errors:  m.errors.Load(),
		LastRun: lr,
	}
}
