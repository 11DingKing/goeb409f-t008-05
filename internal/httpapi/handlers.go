package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"microgrid/internal/application"
	"microgrid/internal/domain"
)

type handlers struct {
	svc *application.Service
}

// Request/response DTOs.

type commandReq struct {
	Kind       string `json:"kind"`
	GridLostAt string `json:"grid_lost_at"`
}

type confirmReq struct {
	Party         string  `json:"party"`
	PhaseAngleDeg float64 `json:"phase_angle_deg"`
	FrequencyHz   float64 `json:"frequency_hz"`
	VoltagePct    float64 `json:"voltage_pct"`
}

type breakerReq struct {
	Success bool `json:"success"`
}

type deviationReq struct {
	WindOutput   float64 `json:"wind_output"`
	LoadForecast float64 `json:"load_forecast"`
}

type eventResp struct {
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurred_at"`
	Detail     string    `json:"detail,omitempty"`
}

type processResp struct {
	ID                string      `json:"id"`
	Kind              string      `json:"kind"`
	State             string      `json:"state"`
	Cycle             int         `json:"cycle"`
	CreatedAt         time.Time   `json:"created_at"`
	UpdatedAt         time.Time   `json:"updated_at"`
	GridLostAt        time.Time   `json:"grid_lost_at,omitempty"`
	CommandIssuedAt   time.Time   `json:"command_issued_at"`
	CommandOverdue    bool        `json:"command_overdue"`
	BreakerAttempts   int         `json:"breaker_attempts"`
	ConfirmedParties  []string    `json:"confirmed_parties"`
	DeviationDetected bool        `json:"deviation_detected"`
	DeviationValue    float64     `json:"deviation_value,omitempty"`
	DeviationDeadline time.Time   `json:"deviation_deadline,omitempty"`
	DieselStarted     bool        `json:"diesel_started"`
	EmergencyNotified bool        `json:"emergency_notified"`
	Events            []eventResp `json:"events"`
}

func toResp(p *domain.BlackStartProcess) processResp {
	parties := p.ConfirmedParties()
	cp := make([]string, 0, len(parties))
	for _, pt := range parties {
		cp = append(cp, string(pt))
	}
	events := make([]eventResp, 0, len(p.Events))
	for _, e := range p.Events {
		events = append(events, eventResp{Type: string(e.Type), OccurredAt: e.OccurredAt, Detail: e.Detail})
	}
	return processResp{
		ID:                p.ID,
		Kind:              string(p.Kind),
		State:             string(p.State),
		Cycle:             p.Cycle,
		CreatedAt:         p.CreatedAt,
		UpdatedAt:         p.UpdatedAt,
		GridLostAt:        p.GridLostAt,
		CommandIssuedAt:   p.CommandIssuedAt,
		CommandOverdue:    p.CommandOverdueRecorded,
		BreakerAttempts:   p.BreakerAttempts,
		ConfirmedParties:  cp,
		DeviationDetected: p.DeviationDetected,
		DeviationValue:    p.DeviationValue,
		DeviationDeadline: p.DeviationDeadline,
		DieselStarted:     p.DieselStarted,
		EmergencyNotified: p.EmergencyNotified,
		Events:            events,
	}
}

func (h *handlers) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handlers) commandBlackStart(w http.ResponseWriter, r *http.Request) {
	var req commandReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind := domain.CommandKind(req.Kind)
	if kind != domain.KindReal && kind != domain.KindDrill {
		writeError(w, http.StatusBadRequest, "kind must be 'real' or 'drill'")
		return
	}
	var gridLostAt time.Time
	if req.GridLostAt != "" {
		t, err := time.Parse(time.RFC3339, req.GridLostAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid grid_lost_at, expected RFC3339")
			return
		}
		gridLostAt = t
	}
	p, err := h.svc.CommandBlackStart(r.Context(), kind, gridLostAt)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toResp(p))
}

func (h *handlers) listProcesses(w http.ResponseWriter, r *http.Request) {
	procs, err := h.svc.List(r.Context())
	if err != nil {
		writeAppError(w, err)
		return
	}
	out := make([]processResp, 0, len(procs))
	for _, p := range procs {
		out = append(out, toResp(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handlers) getProcess(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toResp(p))
}

func (h *handlers) markStorage(w http.ResponseWriter, r *http.Request) {
	h.advanceOp(w, r, h.svc.MarkStorageOnline)
}

func (h *handlers) markWind(w http.ResponseWriter, r *http.Request) {
	h.advanceOp(w, r, h.svc.MarkWindReady)
}

func (h *handlers) markPV(w http.ResponseWriter, r *http.Request) {
	h.advanceOp(w, r, h.svc.MarkPVReady)
}

func (h *handlers) completeLoads(w http.ResponseWriter, r *http.Request) {
	h.advanceOp(w, r, h.svc.CompleteLoadRestore)
}

func (h *handlers) beginSync(w http.ResponseWriter, r *http.Request) {
	h.advanceOp(w, r, h.svc.BeginSynchronization)
}

func (h *handlers) confirmSync(w http.ResponseWriter, r *http.Request) {
	var req confirmReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	party := domain.Party(req.Party)
	switch party {
	case domain.PartyStorage, domain.PartyWind, domain.PartyPV:
	default:
		writeError(w, http.StatusBadRequest, "party must be storage, wind or pv")
		return
	}
	cond := domain.SynchronizationCondition{
		PhaseAngleDeg: req.PhaseAngleDeg,
		FrequencyHz:   req.FrequencyHz,
		VoltagePct:    req.VoltagePct,
	}
	if err := h.svc.ConfirmSync(r.Context(), r.PathValue("id"), party, cond); err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "confirmed"})
}

func (h *handlers) closeBreaker(w http.ResponseWriter, r *http.Request) {
	var req breakerReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svc.CloseBreaker(r.Context(), r.PathValue("id"), req.Success); err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "closed", "success": req.Success})
}

func (h *handlers) reportDeviation(w http.ResponseWriter, r *http.Request) {
	var req deviationReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svc.ReportDeviation(r.Context(), r.PathValue("id"), req.WindOutput, req.LoadForecast); err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}

func (h *handlers) startDiesel(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.StartDiesel(r.Context(), r.PathValue("id")); err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "diesel_started"})
}

// advanceOp runs a body-less transition for the process id in the path.
func (h *handlers) advanceOp(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, id string) error) {
	if err := fn(r.Context(), r.PathValue("id")); err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func decodeJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeAppError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case domain.IsStateError(err):
		writeError(w, http.StatusConflict, err.Error())
	case domain.IsValidationError(err):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
