package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"microgrid/internal/domain"
	"microgrid/internal/store"
)

func fixedTime() time.Time {
	return time.Date(2026, 8, 16, 20, 0, 0, 0, time.UTC)
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func newTestService(t *testing.T) (*Service, *fakeClock) {
	t.Helper()
	fc := &fakeClock{t: fixedTime()}
	counter := 0
	s := NewService(store.NewMemory(),
		WithClock(fc.now),
		WithIDGen(func() string {
			counter++
			return "bsp-test-" + itoa(counter)
		}),
	)
	return s, fc
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func advanceToOffGrid(t *testing.T, ctx context.Context, s *Service, id string) {
	t.Helper()
	steps := []func() error{
		func() error { return s.MarkStorageOnline(ctx, id) },
		func() error { return s.MarkWindReady(ctx, id) },
		func() error { return s.MarkPVReady(ctx, id) },
		func() error { return s.CompleteLoadRestore(ctx, id) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("advance step %d: %v", i, err)
		}
	}
}

func syncConfirmAll(t *testing.T, ctx context.Context, s *Service, id string) {
	t.Helper()
	if err := s.BeginSynchronization(ctx, id); err != nil {
		t.Fatalf("begin sync: %v", err)
	}
	c := domain.SynchronizationCondition{PhaseAngleDeg: 9, FrequencyHz: 0.05, VoltagePct: 3}
	for _, party := range domain.AllParties() {
		if err := s.ConfirmSync(ctx, id, party, c); err != nil {
			t.Fatalf("confirm %s: %v", party, err)
		}
	}
}

func TestService_DrillSuspendedWhenRealActive(t *testing.T) {
	s, fc := newTestService(t)
	ctx := context.Background()

	real, err := s.CommandBlackStart(ctx, domain.KindReal, fc.now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	advanceToOffGrid(t, ctx, s, real.ID)

	drill, err := s.CommandBlackStart(ctx, domain.KindDrill, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if drill.State != domain.StateDrillSuspended {
		t.Fatalf("expected drill_suspended, got %s", drill.State)
	}
	if drill.IsActive() {
		t.Fatal("suspended drill should be inactive")
	}
}

func TestService_RealPreemptsActiveDrill(t *testing.T) {
	s, fc := newTestService(t)
	ctx := context.Background()

	drill, err := s.CommandBlackStart(ctx, domain.KindDrill, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	advanceToOffGrid(t, ctx, s, drill.ID)

	real, err := s.CommandBlackStart(ctx, domain.KindReal, fc.now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if real.Kind != domain.KindReal || real.State != domain.StateBlackStartCommanded {
		t.Fatalf("real switch should start, got kind=%s state=%s", real.Kind, real.State)
	}

	got, err := s.Get(ctx, drill.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.StateDrillSuspended {
		t.Fatalf("active drill should be suspended by real switch, got %s", got.State)
	}
}

func TestService_IdempotentAdvance(t *testing.T) {
	s, fc := newTestService(t)
	ctx := context.Background()
	p, err := s.CommandBlackStart(ctx, domain.KindReal, fc.now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkStorageOnline(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkStorageOnline(ctx, p.ID); err != nil {
		t.Fatalf("repeated advance should be idempotent: %v", err)
	}
	got, _ := s.Get(ctx, p.ID)
	if got.State != domain.StateStorageOnline {
		t.Fatalf("expected storage_online, got %s", got.State)
	}
}

func TestService_CloseBreakerFlow(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	p, err := s.CommandBlackStart(ctx, domain.KindReal, fixedTime().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	advanceToOffGrid(t, ctx, s, p.ID)
	syncConfirmAll(t, ctx, s, p.ID)

	if err := s.CloseBreaker(ctx, p.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, p.ID)
	if got.State != domain.StateGridConnected {
		t.Fatalf("expected grid_connected, got %s", got.State)
	}
	if got.BreakerAttempts != 0 {
		t.Fatalf("expected attempts reset, got %d", got.BreakerAttempts)
	}
}

func TestService_BreakerFailureRecovery(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	p, err := s.CommandBlackStart(ctx, domain.KindReal, fixedTime().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	advanceToOffGrid(t, ctx, s, p.ID)

	syncConfirmAll(t, ctx, s, p.ID)
	if err := s.CloseBreaker(ctx, p.ID, false); err != nil {
		t.Fatalf("first failure: %v", err)
	}
	syncConfirmAll(t, ctx, s, p.ID)
	if err := s.CloseBreaker(ctx, p.ID, false); err != nil {
		t.Fatalf("second failure: %v", err)
	}

	got, _ := s.Get(ctx, p.ID)
	if got.State != domain.StateBlackStartCommanded {
		t.Fatalf("expected restart, got %s", got.State)
	}
	if got.Cycle != 2 {
		t.Fatalf("expected cycle 2, got %d", got.Cycle)
	}
	if !got.EmergencyNotified {
		t.Fatal("expected emergency notified")
	}
}

func TestService_CheckDeadlinesDieselOverdue(t *testing.T) {
	s, fc := newTestService(t)
	ctx := context.Background()
	p, err := s.CommandBlackStart(ctx, domain.KindReal, fc.now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	advanceToOffGrid(t, ctx, s, p.ID)

	if err := s.ReportDeviation(ctx, p.ID, 80, 100); err != nil {
		t.Fatal(err)
	}
	fc.advance(5 * time.Minute)
	if err := s.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, p.ID)
	if hasEvent(got, domain.EventDieselOverdue) {
		t.Fatal("should not be overdue before 10min window")
	}

	fc.advance(6 * time.Minute)
	if err := s.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, p.ID)
	if !hasEvent(got, domain.EventDieselOverdue) {
		t.Fatal("should be overdue after 11min")
	}
}

func TestService_NotFound(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	if _, err := s.Get(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := s.MarkStorageOnline(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_ConcurrentCommandsAreSerialized(t *testing.T) {
	s, fc := newTestService(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	const n = 20
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			kind := domain.KindReal
			if i%2 == 0 {
				kind = domain.KindDrill
			}
			p, err := s.CommandBlackStart(ctx, kind, fc.now().Add(-time.Minute))
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			ids[i] = p.ID
		}(i)
	}
	wg.Wait()

	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != n {
		t.Fatalf("expected %d processes, got %d", n, len(list))
	}
}

func hasEvent(p *domain.BlackStartProcess, typ domain.EventType) bool {
	for _, e := range p.Events {
		if e.Type == typ {
			return true
		}
	}
	return false
}
