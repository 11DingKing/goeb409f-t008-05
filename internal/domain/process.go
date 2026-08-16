package domain

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// CommandKind distinguishes a real off-grid switch from a drill.
type CommandKind string

const (
	KindReal  CommandKind = "real"
	KindDrill CommandKind = "drill"
)

// State enumerates the black-start process lifecycle states.
type State string

const (
	StateBlackStartCommanded State = "black_start_commanded"
	StateStorageOnline       State = "storage_online"
	StateWindReady           State = "wind_ready"
	StatePVReady             State = "pv_ready"
	StateOffGridRunning      State = "off_grid_running"
	StateSynchronizing       State = "synchronizing"
	StateSyncConfirmed       State = "sync_confirmed"
	StateGridConnected       State = "grid_connected"
	StateReverting           State = "reverting"
	StateDrillSuspended      State = "drill_suspended"
)

// ErrNotFound is returned when a process id does not exist.
var ErrNotFound = errors.New("process not found")

// StateError indicates an invalid state transition attempt.
type StateError struct {
	Op   string
	From State
	To   State
}

func (e *StateError) Error() string {
	return fmt.Sprintf("invalid transition %q: from state %q, expected %q", e.Op, e.From, e.To)
}

// IsStateError reports whether err is a StateError.
func IsStateError(err error) bool {
	var se *StateError
	return errors.As(err, &se)
}

// ValidationError indicates invalid input values.
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return e.Msg }

// IsValidationError reports whether err is a ValidationError.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

func validationError(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Settings are the tunable business parameters governing a process.
type Settings struct {
	Limits             SyncLimits
	DeviationThreshold float64
	BlackStartWindow   time.Duration
	DieselStartWindow  time.Duration
	MaxBreakerFailures int
}

// DefaultSettings returns the business-mandated defaults.
func DefaultSettings() Settings {
	return Settings{
		Limits:             DefaultSyncLimits(),
		DeviationThreshold: 0.15,
		BlackStartWindow:   5 * time.Minute,
		DieselStartWindow:  10 * time.Minute,
		MaxBreakerFailures: 2,
	}
}

// BlackStartProcess is the aggregate root tracking one black-start operation.
type BlackStartProcess struct {
	ID        string
	Kind      CommandKind
	State     State
	Cycle     int
	CreatedAt time.Time
	UpdatedAt time.Time

	GridLostAt             time.Time
	CommandIssuedAt        time.Time
	CommandDeadline        time.Time
	CommandOverdueRecorded bool

	syncConfirmations map[Party]SynchronizationCondition
	syncConfirmedAt   map[Party]time.Time
	BreakerAttempts   int
	LastBreakerAt     time.Time

	DeviationDetected     bool
	DeviationValue        float64
	DeviationDeadline     time.Time
	DieselStarted         bool
	DieselStartedAt       time.Time
	DieselOverdueRecorded bool

	EmergencyNotified bool

	Events   []Event
	settings Settings
}

// NewProcess creates a process that has just received a black-start command.
func NewProcess(id string, kind CommandKind, now time.Time, gridLostAt time.Time, settings Settings) *BlackStartProcess {
	if settings.MaxBreakerFailures <= 0 {
		settings.MaxBreakerFailures = DefaultSettings().MaxBreakerFailures
	}
	p := &BlackStartProcess{
		ID:                id,
		Kind:              kind,
		State:             StateBlackStartCommanded,
		Cycle:             1,
		CreatedAt:         now,
		UpdatedAt:         now,
		GridLostAt:        gridLostAt,
		CommandIssuedAt:   now,
		syncConfirmations: make(map[Party]SynchronizationCondition),
		syncConfirmedAt:   make(map[Party]time.Time),
		settings:          settings,
	}
	if kind == KindReal && !gridLostAt.IsZero() {
		p.CommandDeadline = gridLostAt.Add(settings.BlackStartWindow)
		if now.After(p.CommandDeadline) {
			p.CommandOverdueRecorded = true
			p.record(now, EventBlackStartOverdue, fmt.Sprintf("command issued %s after the %s window", now.Sub(p.CommandDeadline), settings.BlackStartWindow))
		}
	}
	p.record(now, EventBlackStartCommanded, fmt.Sprintf("kind=%s cycle=%d", kind, p.Cycle))
	return p
}

// NewSuspendedDrill creates a drill process that was superseded by an active
// real off-grid switch before it could start.
func NewSuspendedDrill(id string, now time.Time, settings Settings) *BlackStartProcess {
	p := &BlackStartProcess{
		ID:                id,
		Kind:              KindDrill,
		State:             StateDrillSuspended,
		Cycle:             1,
		CreatedAt:         now,
		UpdatedAt:         now,
		syncConfirmations: make(map[Party]SynchronizationCondition),
		syncConfirmedAt:   make(map[Party]time.Time),
		settings:          settings,
	}
	p.record(now, EventBlackStartCommanded, "kind=drill")
	p.record(now, EventDrillSuspended, "superseded by active real off-grid switch")
	return p
}

// Clone returns a deep copy of the process so callers cannot mutate stored state.
func (p *BlackStartProcess) Clone() *BlackStartProcess {
	if p == nil {
		return nil
	}
	c := *p
	if p.syncConfirmations != nil {
		c.syncConfirmations = make(map[Party]SynchronizationCondition, len(p.syncConfirmations))
		for k, v := range p.syncConfirmations {
			c.syncConfirmations[k] = v
		}
	}
	if p.syncConfirmedAt != nil {
		c.syncConfirmedAt = make(map[Party]time.Time, len(p.syncConfirmedAt))
		for k, v := range p.syncConfirmedAt {
			c.syncConfirmedAt[k] = v
		}
	}
	if p.Events != nil {
		c.Events = make([]Event, len(p.Events))
		copy(c.Events, p.Events)
	}
	return &c
}

// IsActive reports whether the process is still in an operational state.
func (p *BlackStartProcess) IsActive() bool {
	switch p.State {
	case StateGridConnected, StateDrillSuspended:
		return false
	default:
		return true
	}
}

// ConfirmedParties returns the parties that have confirmed synchronization.
func (p *BlackStartProcess) ConfirmedParties() []Party {
	out := make([]Party, 0, len(p.syncConfirmations))
	for _, party := range AllParties() {
		if _, ok := p.syncConfirmations[party]; ok {
			out = append(out, party)
		}
	}
	return out
}

func rankOf(s State) int {
	switch s {
	case StateBlackStartCommanded:
		return 1
	case StateStorageOnline:
		return 2
	case StateWindReady:
		return 3
	case StatePVReady:
		return 4
	case StateOffGridRunning:
		return 5
	case StateSynchronizing:
		return 6
	case StateSyncConfirmed:
		return 7
	case StateGridConnected:
		return 8
	default:
		return 0
	}
}

func (p *BlackStartProcess) allConfirmed() bool {
	for _, party := range AllParties() {
		if _, ok := p.syncConfirmations[party]; !ok {
			return false
		}
	}
	return true
}

func (p *BlackStartProcess) record(now time.Time, t EventType, detail string) {
	p.Events = append(p.Events, Event{Type: t, OccurredAt: now, Detail: detail})
}

func (p *BlackStartProcess) touch(now time.Time) {
	p.UpdatedAt = now
}

// MarkStorageOnline brings the grid-forming storage battery cabin online as
// the sole black-start power source.
func (p *BlackStartProcess) MarkStorageOnline(now time.Time) error {
	if rankOf(p.State) >= rankOf(StateStorageOnline) {
		return nil
	}
	if p.State != StateBlackStartCommanded {
		return &StateError{Op: "mark storage online", From: p.State, To: StateBlackStartCommanded}
	}
	p.State = StateStorageOnline
	p.record(now, EventStorageOnline, "grid-forming storage battery cabin online")
	p.touch(now)
	return nil
}

// MarkWindReady brings up the wind collection station reactive support.
func (p *BlackStartProcess) MarkWindReady(now time.Time) error {
	if rankOf(p.State) >= rankOf(StateWindReady) {
		return nil
	}
	if p.State != StateStorageOnline {
		return &StateError{Op: "mark wind ready", From: p.State, To: StateStorageOnline}
	}
	p.State = StateWindReady
	p.record(now, EventWindReady, "wind collection station reactive support up")
	p.touch(now)
	return nil
}

// MarkPVReady brings up the PV station reactive support.
func (p *BlackStartProcess) MarkPVReady(now time.Time) error {
	if rankOf(p.State) >= rankOf(StatePVReady) {
		return nil
	}
	if p.State != StateWindReady {
		return &StateError{Op: "mark pv ready", From: p.State, To: StateWindReady}
	}
	p.State = StatePVReady
	p.record(now, EventPVReady, "pv station reactive support up")
	p.touch(now)
	return nil
}

// CompleteLoadRestore finishes restoring jurisdiction bus loads and enters
// off-grid independent operation.
func (p *BlackStartProcess) CompleteLoadRestore(now time.Time) error {
	if rankOf(p.State) >= rankOf(StateOffGridRunning) {
		return nil
	}
	if p.State != StatePVReady {
		return &StateError{Op: "complete load restore", From: p.State, To: StatePVReady}
	}
	p.State = StateOffGridRunning
	p.record(now, EventLoadsRestored, "jurisdiction bus loads restored; off-grid running")
	p.touch(now)
	return nil
}

// BeginSynchronization starts the auto-synchronization check phase.
func (p *BlackStartProcess) BeginSynchronization(now time.Time) error {
	if p.State == StateSynchronizing || p.State == StateSyncConfirmed {
		return nil
	}
	if p.State != StateOffGridRunning {
		return &StateError{Op: "begin synchronization", From: p.State, To: StateOffGridRunning}
	}
	p.State = StateSynchronizing
	p.touch(now)
	return nil
}

// ConfirmSync records one party's synchronization confirmation. Re-confirming
// the same party is idempotent. When all three parties have confirmed, the
// process advances to SyncConfirmed.
func (p *BlackStartProcess) ConfirmSync(party Party, cond SynchronizationCondition, now time.Time) error {
	if p.State != StateSynchronizing && p.State != StateSyncConfirmed {
		return &StateError{Op: "confirm sync", From: p.State, To: StateSynchronizing}
	}
	if err := cond.Validate(p.settings.Limits); err != nil {
		return err
	}
	p.syncConfirmations[party] = cond
	p.syncConfirmedAt[party] = now
	p.record(now, EventSyncConfirmed, fmt.Sprintf("party=%s %s", party, cond))
	if p.allConfirmed() && p.State != StateSyncConfirmed {
		p.State = StateSyncConfirmed
	}
	p.touch(now)
	return nil
}

// CloseBreaker closes the breaker to reconnect to the main grid. It may only
// be called after all three parties have confirmed synchronization.
func (p *BlackStartProcess) CloseBreaker(now time.Time) error {
	if p.State == StateGridConnected {
		return nil
	}
	if p.State != StateSyncConfirmed {
		return &StateError{Op: "close breaker", From: p.State, To: StateSyncConfirmed}
	}
	if !p.allConfirmed() {
		return validationError("cannot close breaker: %d/3 parties confirmed", len(p.syncConfirmations))
	}
	p.State = StateGridConnected
	p.BreakerAttempts = 0
	p.record(now, EventBreakerClosed, "main grid reconnected")
	p.touch(now)
	return nil
}

// ReportBreakerFailure records a failed breaker close attempt. After the first
// failure the parties must re-confirm. After MaxBreakerFailures consecutive
// failures the process reverts to off-grid and re-executes the black-start
// sequence, and the county emergency duty office is notified.
func (p *BlackStartProcess) ReportBreakerFailure(now time.Time) error {
	if p.State != StateSyncConfirmed {
		return &StateError{Op: "report breaker failure", From: p.State, To: StateSyncConfirmed}
	}
	p.BreakerAttempts++
	p.LastBreakerAt = now
	p.record(now, EventBreakerCloseFailed, fmt.Sprintf("attempt=%d", p.BreakerAttempts))
	if p.BreakerAttempts >= p.settings.MaxBreakerFailures {
		if err := p.Revert(now); err != nil {
			return err
		}
		if err := p.RestartBlackStart(now); err != nil {
			return err
		}
	} else {
		p.syncConfirmations = make(map[Party]SynchronizationCondition)
		p.syncConfirmedAt = make(map[Party]time.Time)
		p.State = StateSynchronizing
	}
	p.touch(now)
	return nil
}

// Revert falls back to off-grid running and notifies the emergency duty office.
func (p *BlackStartProcess) Revert(now time.Time) error {
	if p.State != StateSyncConfirmed {
		return &StateError{Op: "revert", From: p.State, To: StateSyncConfirmed}
	}
	p.State = StateOffGridRunning
	p.EmergencyNotified = true
	p.record(now, EventReverting, fmt.Sprintf("after %d consecutive breaker failures", p.BreakerAttempts))
	p.record(now, EventEmergencyNotified, "county emergency duty office notified")
	p.touch(now)
	return nil
}

// RestartBlackStart re-executes the black-start sequence from the beginning on
// a fresh cycle, clearing prior synchronization and breaker state.
func (p *BlackStartProcess) RestartBlackStart(now time.Time) error {
	if p.State != StateOffGridRunning {
		return &StateError{Op: "restart black start", From: p.State, To: StateOffGridRunning}
	}
	p.Cycle++
	p.syncConfirmations = make(map[Party]SynchronizationCondition)
	p.syncConfirmedAt = make(map[Party]time.Time)
	p.BreakerAttempts = 0
	// The deviation and diesel bookkeeping belongs to the cycle it was recorded
	// in; the new cycle has to arm and report its own diesel deadline.
	p.DeviationDetected = false
	p.DeviationValue = 0
	p.DeviationDeadline = time.Time{}
	p.DieselStarted = false
	p.DieselStartedAt = time.Time{}
	p.DieselOverdueRecorded = false
	p.State = StateBlackStartCommanded
	p.record(now, EventBlackStartRestarted, fmt.Sprintf("cycle=%d", p.Cycle))
	p.touch(now)
	return nil
}

// ReportDeviation compares wind output against load forecast. When the relative
// deviation exceeds the threshold it arms a diesel-start deadline.
func (p *BlackStartProcess) ReportDeviation(windOutput, loadForecast float64, now time.Time) error {
	if p.State != StateOffGridRunning {
		return &StateError{Op: "report deviation", From: p.State, To: StateOffGridRunning}
	}
	if loadForecast <= 0 {
		return validationError("load forecast must be positive")
	}
	dev := math.Abs(windOutput-loadForecast) / loadForecast
	if dev > p.settings.DeviationThreshold {
		p.DeviationDetected = true
		p.DeviationValue = dev
		p.DeviationDeadline = now.Add(p.settings.DieselStartWindow)
		p.record(now, EventDeviationDetected, fmt.Sprintf("deviation=%.1f%% threshold=%.1f%% diesel_deadline_in=%s", dev*100, p.settings.DeviationThreshold*100, p.settings.DieselStartWindow))
	}
	p.touch(now)
	return nil
}

// StartDiesel starts the backup diesel generator to smooth wind fluctuation.
func (p *BlackStartProcess) StartDiesel(now time.Time) error {
	if p.State != StateOffGridRunning {
		return &StateError{Op: "start diesel", From: p.State, To: StateOffGridRunning}
	}
	if !p.DeviationDetected {
		return validationError("no deviation detected; diesel start not required")
	}
	if p.DieselStarted {
		return nil
	}
	p.DieselStarted = true
	p.DieselStartedAt = now
	detail := "backup diesel generator started"
	if now.After(p.DeviationDeadline) {
		detail = fmt.Sprintf("%s (late by %s)", detail, now.Sub(p.DeviationDeadline))
	}
	p.record(now, EventDieselStarted, detail)
	p.touch(now)
	return nil
}

// CheckDieselDeadline records an overdue event if the diesel generator was not
// started within its window. It is idempotent.
func (p *BlackStartProcess) CheckDieselDeadline(now time.Time) bool {
	if !p.DeviationDetected || p.DieselStarted || p.DieselOverdueRecorded {
		return false
	}
	if now.After(p.DeviationDeadline) {
		p.DieselOverdueRecorded = true
		p.record(now, EventDieselOverdue, fmt.Sprintf("diesel not started within %s of deviation", p.settings.DieselStartWindow))
		p.touch(now)
		return true
	}
	return false
}

// Suspend transitions a drill process to suspended (superseded by a real switch).
func (p *BlackStartProcess) Suspend(now time.Time) error {
	if p.State == StateDrillSuspended || p.State == StateGridConnected {
		return &StateError{Op: "suspend", From: p.State, To: StateDrillSuspended}
	}
	p.State = StateDrillSuspended
	p.record(now, EventDrillSuspended, "superseded by real off-grid switch")
	p.touch(now)
	return nil
}
