package domain

import (
	"testing"
	"time"
)

func fixedTime() time.Time {
	return time.Date(2026, 8, 16, 20, 0, 0, 0, time.UTC)
}

func validCondition() SynchronizationCondition {
	return SynchronizationCondition{PhaseAngleDeg: 9, FrequencyHz: 0.05, VoltagePct: 3}
}

func newAtOffGrid(t *testing.T, now time.Time) *BlackStartProcess {
	t.Helper()
	p := NewProcess("bsp-x", KindReal, now, now.Add(-time.Minute), DefaultSettings())
	for _, fn := range []func() error{
		func() error { return p.MarkStorageOnline(now) },
		func() error { return p.MarkWindReady(now) },
		func() error { return p.MarkPVReady(now) },
		func() error { return p.CompleteLoadRestore(now) },
	} {
		if err := fn(); err != nil {
			t.Fatalf("setup transition failed: %v", err)
		}
	}
	return p
}

func confirmAll(t *testing.T, p *BlackStartProcess, now time.Time) {
	t.Helper()
	if err := p.BeginSynchronization(now); err != nil {
		t.Fatalf("begin sync: %v", err)
	}
	for _, party := range AllParties() {
		if err := p.ConfirmSync(party, validCondition(), now); err != nil {
			t.Fatalf("confirm %s: %v", party, err)
		}
	}
	if p.State != StateSyncConfirmed {
		t.Fatalf("expected sync_confirmed, got %s", p.State)
	}
}

func TestProcess_BlackStartHappyPath(t *testing.T) {
	now := fixedTime()
	p := NewProcess("bsp-1", KindReal, now, now.Add(-2*time.Minute), DefaultSettings())

	if err := p.MarkStorageOnline(now); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkWindReady(now); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkPVReady(now); err != nil {
		t.Fatal(err)
	}
	if err := p.CompleteLoadRestore(now); err != nil {
		t.Fatal(err)
	}
	if p.State != StateOffGridRunning {
		t.Fatalf("expected off_grid_running, got %s", p.State)
	}
	if err := p.BeginSynchronization(now); err != nil {
		t.Fatal(err)
	}
	for _, party := range AllParties() {
		if err := p.ConfirmSync(party, validCondition(), now); err != nil {
			t.Fatalf("confirm %s: %v", party, err)
		}
	}
	if err := p.CloseBreaker(now); err != nil {
		t.Fatal(err)
	}
	if p.State != StateGridConnected {
		t.Fatalf("expected grid_connected, got %s", p.State)
	}
	if p.CommandOverdueRecorded {
		t.Fatal("command issued within window should not be overdue")
	}
	if !hasEvent(p, EventBreakerClosed) {
		t.Fatal("expected breaker_closed event")
	}
}

func TestProcess_InvalidTransition(t *testing.T) {
	now := fixedTime()
	p := NewProcess("bsp-2", KindReal, now, now.Add(-time.Minute), DefaultSettings())
	if err := p.MarkWindReady(now); err == nil {
		t.Fatal("expected error marking wind ready before storage")
	} else if !IsStateError(err) {
		t.Fatalf("expected StateError, got %T", err)
	}
	if err := p.CloseBreaker(now); err == nil {
		t.Fatal("expected error closing breaker before synchronization")
	}
}

func TestProcess_BreakerFailureRevertAndRestart(t *testing.T) {
	now := fixedTime()
	p := newAtOffGrid(t, now)

	confirmAll(t, p, now)
	if err := p.ReportBreakerFailure(now); err != nil {
		t.Fatalf("first failure: %v", err)
	}
	if p.BreakerAttempts != 1 || p.State != StateSynchronizing {
		t.Fatalf("after 1st failure: attempts=%d state=%s", p.BreakerAttempts, p.State)
	}
	if !hasEvent(p, EventBreakerCloseFailed) {
		t.Fatal("expected breaker_close_failed event")
	}

	confirmAll(t, p, now)
	if err := p.ReportBreakerFailure(now); err != nil {
		t.Fatalf("second failure: %v", err)
	}
	if p.State != StateBlackStartCommanded {
		t.Fatalf("expected restart to black_start_commanded, got %s", p.State)
	}
	if p.Cycle != 2 {
		t.Fatalf("expected cycle 2, got %d", p.Cycle)
	}
	if !p.EmergencyNotified {
		t.Fatal("expected emergency notified")
	}
	if !hasEvent(p, EventReverting) || !hasEvent(p, EventEmergencyNotified) || !hasEvent(p, EventBlackStartRestarted) {
		t.Fatal("expected revert/emergency/restart events")
	}
	if p.BreakerAttempts != 0 {
		t.Fatalf("expected attempts reset to 0, got %d", p.BreakerAttempts)
	}
}

func TestProcess_SyncConfirmationIdempotent(t *testing.T) {
	now := fixedTime()
	p := newAtOffGrid(t, now)
	if err := p.BeginSynchronization(now); err != nil {
		t.Fatal(err)
	}
	c := validCondition()
	if err := p.ConfirmSync(PartyStorage, c, now); err != nil {
		t.Fatal(err)
	}
	if err := p.ConfirmSync(PartyStorage, c, now); err != nil {
		t.Fatalf("re-confirm should be idempotent: %v", err)
	}
	if p.State != StateSynchronizing {
		t.Fatalf("expected synchronizing with 1 party, got %s", p.State)
	}
	if err := p.ConfirmSync(PartyWind, validCondition(), now); err != nil {
		t.Fatal(err)
	}
	if err := p.ConfirmSync(PartyPV, validCondition(), now); err != nil {
		t.Fatal(err)
	}
	if p.State != StateSyncConfirmed {
		t.Fatalf("expected sync_confirmed, got %s", p.State)
	}
}

func TestProcess_DeviationTriggersDiesel(t *testing.T) {
	now := fixedTime()
	p := newAtOffGrid(t, now)

	if err := p.ReportDeviation(95, 100, now); err != nil {
		t.Fatal(err)
	}
	if p.DeviationDetected {
		t.Fatal("5% deviation should not arm diesel (threshold 15%)")
	}
	if err := p.ReportDeviation(80, 100, now); err != nil {
		t.Fatal(err)
	}
	if !p.DeviationDetected {
		t.Fatal("20% deviation should arm diesel")
	}
	if p.DeviationDeadline.IsZero() {
		t.Fatal("deadline should be set")
	}
	if err := p.StartDiesel(now.Add(5 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !p.DieselStarted {
		t.Fatal("diesel should be started")
	}
}

func TestProcess_DeviationDieselOverdue(t *testing.T) {
	now := fixedTime()
	p := newAtOffGrid(t, now)
	if err := p.ReportDeviation(80, 100, now); err != nil {
		t.Fatal(err)
	}
	if p.CheckDieselDeadline(now.Add(5 * time.Minute)) {
		t.Fatal("should not be overdue before deadline")
	}
	if !p.CheckDieselDeadline(now.Add(11 * time.Minute)) {
		t.Fatal("should be overdue after deadline")
	}
	if !hasEvent(p, EventDieselOverdue) {
		t.Fatal("expected diesel_overdue event")
	}
	if p.CheckDieselDeadline(now.Add(12 * time.Minute)) {
		t.Fatal("overdue should be recorded only once")
	}
}

func TestProcess_DieselWithoutDeviation(t *testing.T) {
	now := fixedTime()
	p := newAtOffGrid(t, now)
	err := p.StartDiesel(now)
	if err == nil {
		t.Fatal("expected error starting diesel without deviation")
	}
	if !IsValidationError(err) {
		t.Fatalf("expected ValidationError, got %T", err)
	}
}

func TestProcess_CommandOverdue(t *testing.T) {
	now := fixedTime()
	gridLost := now.Add(-6 * time.Minute)
	p := NewProcess("bsp-o", KindReal, now, gridLost, DefaultSettings())
	if !p.CommandOverdueRecorded {
		t.Fatal("command issued after 5min window should be overdue")
	}
	if !hasEvent(p, EventBlackStartOverdue) {
		t.Fatal("expected black_start_overdue event")
	}
	drill := NewProcess("bsp-d", KindDrill, now, time.Time{}, DefaultSettings())
	if drill.CommandOverdueRecorded {
		t.Fatal("drill should never be marked overdue")
	}
}

func TestProcess_Suspend(t *testing.T) {
	now := fixedTime()
	p := newAtOffGrid(t, now)
	if err := p.Suspend(now); err != nil {
		t.Fatal(err)
	}
	if p.State != StateDrillSuspended {
		t.Fatalf("expected drill_suspended, got %s", p.State)
	}
	if p.IsActive() {
		t.Fatal("suspended process should be inactive")
	}
}

func hasEvent(p *BlackStartProcess, typ EventType) bool {
	for _, e := range p.Events {
		if e.Type == typ {
			return true
		}
	}
	return false
}
