package domain

import (
	"fmt"
	"math"
)

// Party identifies one of the three parties that must confirm
// auto-synchronization conditions before the breaker may be closed.
type Party string

const (
	PartyStorage Party = "storage" // 储能控制员
	PartyWind    Party = "wind"    // 风电场站值班员
	PartyPV      Party = "pv"      // 光伏场站值班员
)

// AllParties returns the three confirming parties in canonical order.
func AllParties() []Party {
	return []Party{PartyStorage, PartyWind, PartyPV}
}

// SyncLimits are the tolerances for auto-synchronizer acceptance.
type SyncLimits struct {
	MaxPhaseAngleDeg float64 `json:"max_phase_angle_deg"`
	MaxFrequencyHz   float64 `json:"max_frequency_hz"`
	MaxVoltagePct    float64 `json:"max_voltage_pct"`
}

// DefaultSyncLimits returns realistic grid auto-synchronizer tolerances.
func DefaultSyncLimits() SyncLimits {
	return SyncLimits{
		MaxPhaseAngleDeg: 15.0,
		MaxFrequencyHz:   0.1,
		MaxVoltagePct:    5.0,
	}
}

// SynchronizationCondition captures the measured deltas between the
// islanded microgrid and the recovering main grid.
type SynchronizationCondition struct {
	PhaseAngleDeg float64 `json:"phase_angle_deg"`
	FrequencyHz   float64 `json:"frequency_hz"`
	VoltagePct    float64 `json:"voltage_pct"`
}

// Validate checks the condition against the limits, returning a
// ValidationError describing the first offending quantity.
func (c SynchronizationCondition) Validate(limits SyncLimits) error {
	if math.Abs(c.PhaseAngleDeg) > limits.MaxPhaseAngleDeg {
		return validationError("phase angle diff %.2f deg exceeds limit %.2f deg", c.PhaseAngleDeg, limits.MaxPhaseAngleDeg)
	}
	if math.Abs(c.FrequencyHz) > limits.MaxFrequencyHz {
		return validationError("frequency diff %.3f Hz exceeds limit %.3f Hz", c.FrequencyHz, limits.MaxFrequencyHz)
	}
	if math.Abs(c.VoltagePct) > limits.MaxVoltagePct {
		return validationError("voltage diff %.2f%% exceeds limit %.2f%%", c.VoltagePct, limits.MaxVoltagePct)
	}
	return nil
}

// String renders the condition for event detail lines.
func (c SynchronizationCondition) String() string {
	return fmt.Sprintf("phase=%.2fdeg freq=%.3fHz volt=%.2f%%", c.PhaseAngleDeg, c.FrequencyHz, c.VoltagePct)
}
