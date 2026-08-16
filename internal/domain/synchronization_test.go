package domain

import "testing"

func TestSynchronizationCondition_Valid(t *testing.T) {
	cond := SynchronizationCondition{PhaseAngleDeg: 10, FrequencyHz: 0.05, VoltagePct: 3}
	if err := cond.Validate(DefaultSyncLimits()); err != nil {
		t.Fatalf("expected valid condition, got %v", err)
	}
}

func TestSynchronizationCondition_Invalid(t *testing.T) {
	limits := DefaultSyncLimits()
	cases := []struct {
		name string
		cond SynchronizationCondition
	}{
		{"phase too high", SynchronizationCondition{PhaseAngleDeg: 20, FrequencyHz: 0.05, VoltagePct: 3}},
		{"phase too low", SynchronizationCondition{PhaseAngleDeg: -20, FrequencyHz: 0.05, VoltagePct: 3}},
		{"frequency too high", SynchronizationCondition{PhaseAngleDeg: 10, FrequencyHz: 0.2, VoltagePct: 3}},
		{"voltage too high", SynchronizationCondition{PhaseAngleDeg: 10, FrequencyHz: 0.05, VoltagePct: 6}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.cond.Validate(limits); err == nil {
				t.Fatal("expected validation error, got nil")
			} else if !IsValidationError(err) {
				t.Fatalf("expected ValidationError, got %T", err)
			}
		})
	}
}
