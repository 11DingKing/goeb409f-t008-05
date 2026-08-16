package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"microgrid/internal/application"
)

// Server wraps an *http.Server serving the black-start API.
type Server struct {
	srv *http.Server
}

// NewHandler builds the routed HTTP handler for the given service.
func NewHandler(svc *application.Service) http.Handler {
	mux := http.NewServeMux()
	h := &handlers{svc: svc}
	register(mux, h)
	return recoverer(slog.Default())(mux)
}

// New creates a Server bound to addr.
func New(addr string, svc *application.Service) *Server {
	return &Server{
		srv: &http.Server{
			Addr:              addr,
			Handler:           NewHandler(svc),
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func register(mux *http.ServeMux, h *handlers) {
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("POST /api/v1/processes", h.commandBlackStart)
	mux.HandleFunc("GET /api/v1/processes", h.listProcesses)
	mux.HandleFunc("GET /api/v1/processes/{id}", h.getProcess)
	mux.HandleFunc("POST /api/v1/processes/{id}/storage", h.markStorage)
	mux.HandleFunc("POST /api/v1/processes/{id}/wind", h.markWind)
	mux.HandleFunc("POST /api/v1/processes/{id}/pv", h.markPV)
	mux.HandleFunc("POST /api/v1/processes/{id}/loads", h.completeLoads)
	mux.HandleFunc("POST /api/v1/processes/{id}/synchronize", h.beginSync)
	mux.HandleFunc("POST /api/v1/processes/{id}/confirm", h.confirmSync)
	mux.HandleFunc("POST /api/v1/processes/{id}/breaker", h.closeBreaker)
	mux.HandleFunc("POST /api/v1/processes/{id}/deviation", h.reportDeviation)
	mux.HandleFunc("POST /api/v1/processes/{id}/diesel", h.startDiesel)
}

// recoverer returns middleware that recovers from panics and logs them.
func recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered", "path", r.URL.Path, "panic", rec)
					writeError(w, http.StatusInternalServerError, "internal error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
