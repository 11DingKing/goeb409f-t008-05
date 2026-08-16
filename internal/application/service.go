package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"microgrid/internal/domain"
)

// Store is the repository port the application depends on.
type Store interface {
	Save(ctx context.Context, p *domain.BlackStartProcess) error
	Get(ctx context.Context, id string) (*domain.BlackStartProcess, error)
	List(ctx context.Context) ([]*domain.BlackStartProcess, error)
	Active(ctx context.Context) ([]*domain.BlackStartProcess, error)
}

// Service orchestrates black-start commands. All mutations are serialized
// through a single mutex so that concurrency-sensitive decisions (drill vs.
// real priority, deadline checks, breaker recovery) are atomic.
type Service struct {
	store    Store
	mu       sync.Mutex
	clock    func() time.Time
	idGen    func() string
	settings domain.Settings
}

// Option configures a Service.
type Option func(*Service)

// WithClock injects a clock (for tests).
func WithClock(fn func() time.Time) Option {
	return func(s *Service) {
		if fn != nil {
			s.clock = fn
		}
	}
}

// WithIDGen injects an id generator (for tests).
func WithIDGen(fn func() string) Option {
	return func(s *Service) {
		if fn != nil {
			s.idGen = fn
		}
	}
}

// WithSettings overrides the business settings.
func WithSettings(settings domain.Settings) Option {
	return func(s *Service) {
		s.settings = settings
	}
}

// NewService creates a Service with sensible defaults.
func NewService(store Store, opts ...Option) *Service {
	s := &Service{
		store:    store,
		clock:    time.Now,
		idGen:    defaultIDGen,
		settings: domain.DefaultSettings(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func defaultIDGen() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("bsp-%d", time.Now().UnixNano())
	}
	return "bsp-" + hex.EncodeToString(b)
}

// CommandBlackStart issues a black-start command. When a real off-grid switch
// is active, an incoming drill is suspended and recorded rather than started.
// When a real command arrives while a drill is active, the drill is suspended
// and the real switch takes priority.
func (s *Service) CommandBlackStart(ctx context.Context, kind domain.CommandKind, gridLostAt time.Time) (*domain.BlackStartProcess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock()
	active, err := s.store.Active(ctx)
	if err != nil {
		return nil, err
	}

	hasReal := false
	for _, p := range active {
		if p.Kind == domain.KindReal {
			hasReal = true
			break
		}
	}

	if kind == domain.KindDrill && hasReal {
		drill := domain.NewSuspendedDrill(s.idGen(), now, s.settings)
		if err := s.store.Save(ctx, drill); err != nil {
			return nil, err
		}
		return drill, nil
	}

	if kind == domain.KindReal {
		for _, p := range active {
			if p.Kind == domain.KindDrill {
				if err := p.Suspend(now); err == nil {
					_ = s.store.Save(ctx, p)
				}
			}
		}
	}

	p := domain.NewProcess(s.idGen(), kind, now, gridLostAt, s.settings)
	if err := s.store.Save(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Service) apply(ctx context.Context, id string, fn func(*domain.BlackStartProcess, time.Time) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := fn(p, s.clock()); err != nil {
		return err
	}
	return s.store.Save(ctx, p)
}

// MarkStorageOnline advances the process to storage online.
func (s *Service) MarkStorageOnline(ctx context.Context, id string) error {
	return s.apply(ctx, id, func(p *domain.BlackStartProcess, now time.Time) error { return p.MarkStorageOnline(now) })
}

// MarkWindReady advances the process to wind ready.
func (s *Service) MarkWindReady(ctx context.Context, id string) error {
	return s.apply(ctx, id, func(p *domain.BlackStartProcess, now time.Time) error { return p.MarkWindReady(now) })
}

// MarkPVReady advances the process to PV ready.
func (s *Service) MarkPVReady(ctx context.Context, id string) error {
	return s.apply(ctx, id, func(p *domain.BlackStartProcess, now time.Time) error { return p.MarkPVReady(now) })
}

// CompleteLoadRestore enters off-grid running.
func (s *Service) CompleteLoadRestore(ctx context.Context, id string) error {
	return s.apply(ctx, id, func(p *domain.BlackStartProcess, now time.Time) error { return p.CompleteLoadRestore(now) })
}

// BeginSynchronization starts the synchronization phase.
func (s *Service) BeginSynchronization(ctx context.Context, id string) error {
	return s.apply(ctx, id, func(p *domain.BlackStartProcess, now time.Time) error { return p.BeginSynchronization(now) })
}

// ConfirmSync records a party's synchronization confirmation.
func (s *Service) ConfirmSync(ctx context.Context, id string, party domain.Party, cond domain.SynchronizationCondition) error {
	return s.apply(ctx, id, func(p *domain.BlackStartProcess, now time.Time) error {
		return p.ConfirmSync(party, cond, now)
	})
}

// CloseBreaker attempts to close the breaker. On success the grid is
// reconnected; on failure the failure is recorded and may trigger recovery.
func (s *Service) CloseBreaker(ctx context.Context, id string, success bool) error {
	return s.apply(ctx, id, func(p *domain.BlackStartProcess, now time.Time) error {
		if success {
			return p.CloseBreaker(now)
		}
		return p.ReportBreakerFailure(now)
	})
}

// ReportDeviation reports wind output vs load forecast for diesel arming.
func (s *Service) ReportDeviation(ctx context.Context, id string, windOutput, loadForecast float64) error {
	return s.apply(ctx, id, func(p *domain.BlackStartProcess, now time.Time) error {
		return p.ReportDeviation(windOutput, loadForecast, now)
	})
}

// StartDiesel starts the backup diesel generator.
func (s *Service) StartDiesel(ctx context.Context, id string) error {
	return s.apply(ctx, id, func(p *domain.BlackStartProcess, now time.Time) error { return p.StartDiesel(now) })
}

// Get returns a process by id.
func (s *Service) Get(ctx context.Context, id string) (*domain.BlackStartProcess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Get(ctx, id)
}

// List returns all processes.
func (s *Service) List(ctx context.Context) ([]*domain.BlackStartProcess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.List(ctx)
}

// CheckDeadlines scans active processes for expired diesel deadlines and
// records overdue events. It is invoked by the background monitor.
func (s *Service) CheckDeadlines(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	procs, err := s.store.Active(ctx)
	if err != nil {
		return err
	}
	for _, p := range procs {
		if p.CheckDieselDeadline(now) {
			if err := s.store.Save(ctx, p); err != nil {
				return err
			}
		}
	}
	return nil
}
