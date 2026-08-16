package domain

import (
	"testing"
	"time"
)

// TestProcess_RestartStartsCleanDeviationCycle covers the state a new
// black-start cycle begins from after two consecutive breaker-close failures
// force a fall back to off-grid running: the deviation and diesel bookkeeping
// belongs to the cycle it was recorded in, so the new cycle must start from a
// clean picture and must be able to arm and report its own diesel deadline.
func TestProcess_RestartStartsCleanDeviationCycle(t *testing.T) {
	now := fixedTime()
	p := newAtOffGrid(t, now)

	// Cycle 1: a 20% deviation arms the diesel deadline and the crew starts the
	// backup diesel generator.
	if err := p.ReportDeviation(80, 100, now); err != nil {
		t.Fatalf("report deviation: %v", err)
	}
	if !p.DeviationDetected {
		t.Fatal("a 20%% deviation should arm the diesel deadline")
	}
	if err := p.StartDiesel(now.Add(time.Minute)); err != nil {
		t.Fatalf("start diesel: %v", err)
	}

	// Two consecutive breaker-close failures revert to off-grid and re-execute
	// the black-start sequence on a new cycle.
	confirmAll(t, p, now)
	if err := p.ReportBreakerFailure(now); err != nil {
		t.Fatalf("first breaker failure: %v", err)
	}
	confirmAll(t, p, now)
	if err := p.ReportBreakerFailure(now); err != nil {
		t.Fatalf("second breaker failure: %v", err)
	}
	if p.State != StateBlackStartCommanded || p.Cycle != 2 {
		t.Fatalf("expected a restarted cycle 2 in black_start_commanded, got cycle=%d state=%s", p.Cycle, p.State)
	}

	if p.DeviationDetected {
		t.Error("the new cycle still reports the previous cycle's deviation")
	}
	if p.DeviationValue != 0 {
		t.Errorf("the new cycle still reports deviation value %.2f", p.DeviationValue)
	}
	if !p.DeviationDeadline.IsZero() {
		t.Errorf("the new cycle still carries diesel deadline %s", p.DeviationDeadline)
	}
	if p.DieselStarted {
		t.Error("the new cycle still reports the backup diesel generator as started")
	}
	if !p.DieselStartedAt.IsZero() {
		t.Errorf("the new cycle still carries diesel start time %s", p.DieselStartedAt)
	}

	// Walk the new cycle back to off-grid running.
	for i, fn := range []func() error{
		func() error { return p.MarkStorageOnline(now) },
		func() error { return p.MarkWindReady(now) },
		func() error { return p.MarkPVReady(now) },
		func() error { return p.CompleteLoadRestore(now) },
	} {
		if err := fn(); err != nil {
			t.Fatalf("new cycle transition %d: %v", i, err)
		}
	}

	// A fresh deviation in the new cycle that nobody acts on must be reported
	// overdue by the watchdog once its window has passed.
	later := now.Add(30 * time.Minute)
	if err := p.ReportDeviation(70, 100, later); err != nil {
		t.Fatalf("report deviation in the new cycle: %v", err)
	}
	if !p.DeviationDetected {
		t.Fatal("a 30%% deviation in the new cycle should arm the diesel deadline")
	}
	if p.CheckDieselDeadline(later.Add(5 * time.Minute)) {
		t.Error("the new cycle's diesel deadline must not expire after 5 minutes")
	}
	if !p.CheckDieselDeadline(later.Add(11 * time.Minute)) {
		t.Error("the watchdog must report the new cycle's diesel generator overdue after 11 minutes")
	}
	if !hasEvent(p, EventDieselOverdue) {
		t.Error("expected a diesel_overdue event for the new cycle")
	}
}
