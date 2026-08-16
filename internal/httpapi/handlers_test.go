package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"microgrid/internal/application"
	"microgrid/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	svc := application.NewService(store.NewMemory())
	return httptest.NewServer(NewHandler(svc))
}

func do(t *testing.T, method, url string, body []byte) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func mustPost(t *testing.T, url string, body any) {
	t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	code, b := do(t, http.MethodPost, url, payload)
	if code >= 300 {
		t.Fatalf("POST %s: status %d: %s", url, code, string(b))
	}
}

func createProcess(t *testing.T, srv *httptest.Server, kind, gridLostAt string) processResp {
	t.Helper()
	payload, _ := json.Marshal(commandReq{Kind: kind, GridLostAt: gridLostAt})
	_, b := do(t, http.MethodPost, srv.URL+"/api/v1/processes", payload)
	var p processResp
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, string(b))
	}
	return p
}

func getProcess(t *testing.T, srv *httptest.Server, id string) processResp {
	t.Helper()
	_, b := do(t, http.MethodGet, srv.URL+"/api/v1/processes/"+id, nil)
	var p processResp
	json.Unmarshal(b, &p)
	return p
}

func confirmAllHTTP(t *testing.T, srv *httptest.Server, id string) {
	t.Helper()
	mustPost(t, srv.URL+"/api/v1/processes/"+id+"/synchronize", nil)
	for _, party := range []string{"storage", "wind", "pv"} {
		mustPost(t, srv.URL+"/api/v1/processes/"+id+"/confirm", confirmReq{
			Party: party, PhaseAngleDeg: 9, FrequencyHz: 0.05, VoltagePct: 3,
		})
	}
}

func TestHTTP_BlackStartFullFlow(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	_ = context.Background()

	p := createProcess(t, srv, "real", "2026-08-16T19:58:00Z")
	if p.State != "black_start_commanded" {
		t.Fatalf("got state %s", p.State)
	}

	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/storage", nil)
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/wind", nil)
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/pv", nil)
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/loads", nil)
	confirmAllHTTP(t, srv, p.ID)
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/breaker", breakerReq{Success: true})

	got := getProcess(t, srv, p.ID)
	if got.State != "grid_connected" {
		t.Fatalf("expected grid_connected, got %s", got.State)
	}
	if len(got.ConfirmedParties) != 3 {
		t.Fatalf("expected 3 confirmed parties, got %d", len(got.ConfirmedParties))
	}
}

func TestHTTP_DrillSuspended(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	createProcess(t, srv, "real", "2026-08-16T19:58:00Z")
	drill := createProcess(t, srv, "drill", "")
	if drill.State != "drill_suspended" {
		t.Fatalf("expected drill_suspended, got %s", drill.State)
	}
}

func TestHTTP_BreakerFailureRecovery(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	p := createProcess(t, srv, "real", "2026-08-16T19:58:00Z")
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/storage", nil)
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/wind", nil)
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/pv", nil)
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/loads", nil)

	confirmAllHTTP(t, srv, p.ID)
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/breaker", breakerReq{Success: false})
	confirmAllHTTP(t, srv, p.ID)
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/breaker", breakerReq{Success: false})

	got := getProcess(t, srv, p.ID)
	if got.State != "black_start_commanded" {
		t.Fatalf("expected restart, got %s", got.State)
	}
	if got.Cycle != 2 {
		t.Fatalf("expected cycle 2, got %d", got.Cycle)
	}
	if !got.EmergencyNotified {
		t.Fatal("expected emergency notified")
	}
}

func TestHTTP_NotFound(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	code, _ := do(t, http.MethodGet, srv.URL+"/api/v1/processes/missing", nil)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", code)
	}
}

func TestHTTP_ConflictOnBadTransition(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	p := createProcess(t, srv, "real", "2026-08-16T19:58:00Z")
	// attempt to close breaker before any synchronization -> 409
	payload, _ := json.Marshal(breakerReq{Success: true})
	code, b := do(t, http.MethodPost, srv.URL+"/api/v1/processes/"+p.ID+"/breaker", payload)
	if code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", code, string(b))
	}
}

func TestHTTP_InvalidSyncCondition(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	p := createProcess(t, srv, "real", "2026-08-16T19:58:00Z")
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/storage", nil)
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/wind", nil)
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/pv", nil)
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/loads", nil)
	mustPost(t, srv.URL+"/api/v1/processes/"+p.ID+"/synchronize", nil)

	// out-of-range phase angle -> 400
	payload, _ := json.Marshal(confirmReq{Party: "storage", PhaseAngleDeg: 90, FrequencyHz: 0.05, VoltagePct: 3})
	code, b := do(t, http.MethodPost, srv.URL+"/api/v1/processes/"+p.ID+"/confirm", payload)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", code, string(b))
	}
}

func TestHTTP_Health(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	code, b := do(t, http.MethodGet, srv.URL+"/healthz", nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var resp map[string]string
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "ok" {
		t.Fatalf("expected ok, got %q", resp["status"])
	}
}
