package domain

import "time"

// EventType enumerates the domain events emitted by a black-start process.
type EventType string

const (
	EventBlackStartCommanded EventType = "black_start_commanded"
	EventBlackStartOverdue   EventType = "black_start_overdue"
	EventStorageOnline       EventType = "storage_online"
	EventWindReady           EventType = "wind_ready"
	EventPVReady             EventType = "pv_ready"
	EventLoadsRestored       EventType = "loads_restored"
	EventSyncConfirmed       EventType = "sync_confirmed"
	EventBreakerClosed       EventType = "breaker_closed"
	EventBreakerCloseFailed  EventType = "breaker_close_failed"
	EventReverting           EventType = "reverting"
	EventBlackStartRestarted EventType = "black_start_restarted"
	EventEmergencyNotified   EventType = "emergency_notified"
	EventDrillSuspended      EventType = "drill_suspended"
	EventDeviationDetected   EventType = "deviation_detected"
	EventDieselStarted       EventType = "diesel_started"
	EventDieselOverdue       EventType = "diesel_overdue"
)

// Event is an immutable record of something that happened to a process.
type Event struct {
	Type       EventType `json:"type"`
	OccurredAt time.Time `json:"occurred_at"`
	Detail     string    `json:"detail,omitempty"`
}
