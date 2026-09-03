package httpapi

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/inv1sion/evoops/internal/app"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/repository"
)

//go:embed web/*
var assets embed.FS

type Server struct {
	app *app.App
	mux *http.ServeMux
}

func New(application *app.App) *Server {
	s := &Server{app: application, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return recoverMiddleware(loggingMiddleware(s.mux))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.health)
	s.mux.HandleFunc("GET /api/tools", s.listTools)
	s.mux.HandleFunc("GET /api/runs", s.listRuns)
	s.mux.HandleFunc("POST /api/runs", s.createRun)
	s.mux.HandleFunc("POST /api/runs/stream", s.streamRun)
	s.mux.HandleFunc("GET /api/runs/{id}", s.getRun)
	s.mux.HandleFunc("POST /api/runs/{id}/approve", s.approveRun)
	s.mux.HandleFunc("POST /api/runs/{id}/feedback", s.feedback)
	s.mux.HandleFunc("GET /api/stores/{id}/memory", s.getMerchantMemory)
	s.mux.HandleFunc("GET /api/policies", s.listPolicies)
	s.mux.HandleFunc("GET /api/harness/reports", s.listHarnessReports)
	s.mux.HandleFunc("POST /api/harness/run/{version}", s.evaluate)
	s.mux.HandleFunc("GET /api/evolution/runs", s.listEvolutionRuns)
	s.mux.HandleFunc("POST /api/evolution/run", s.runEvolution)
	s.mux.HandleFunc("POST /api/evolution/candidates", s.generateCandidate)
	s.mux.HandleFunc("POST /api/evolution/evaluate/{version}", s.evaluate)
	s.mux.HandleFunc("POST /api/evolution/canary/{version}", s.canary)
	s.mux.HandleFunc("POST /api/evolution/promote/{version}", s.promote)
	s.mux.HandleFunc("POST /api/evolution/rollback", s.rollback)
	web, _ := fs.Sub(assets, "web")
	s.mux.Handle("/", http.FileServer(http.FS(web)))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "evoops", "time": time.Now().UTC(), "tool_calling_enabled": s.app.Config.ToolCallingEnabled && s.app.Config.OpenAIAPIKey != ""})
}

func (s *Server) listTools(w http.ResponseWriter, r *http.Request) {
	infos, err := s.app.Tools.Infos(r.Context())
	respond(w, infos, err)
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	var request domain.DiagnosisRequest
	if !decode(w, r, &request) {
		return
	}
	run, err := s.app.Agent.Run(r.Context(), request)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "run": run})
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) streamRun(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusNotImplemented, fmt.Errorf("streaming is unavailable"))
		return
	}
	var request domain.DiagnosisRequest
	if !decode(w, r, &request) {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	sse(w, "started", map[string]any{"store_id": request.StoreID})
	flusher.Flush()
	run, err := s.app.Agent.Run(r.Context(), request)
	if err != nil {
		sse(w, "error", map[string]any{"error": err.Error(), "run": run})
		flusher.Flush()
		return
	}
	for _, step := range run.Steps {
		sse(w, "step", step)
		flusher.Flush()
	}
	sse(w, "completed", run)
	flusher.Flush()
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 25
	}
	runs, err := s.app.Repo.ListRuns(r.Context(), limit)
	respond(w, runs, err)
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.app.Repo.GetRun(r.Context(), r.PathValue("id"))
	respond(w, run, err)
}

func (s *Server) approveRun(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "approver", "admin") {
		return
	}
	var decision domain.ApprovalDecision
	if !decode(w, r, &decision) {
		return
	}
	decision.Actor = actor(r)
	run, err := s.app.Agent.Approve(r.Context(), r.PathValue("id"), decision)
	respond(w, run, err)
}

func (s *Server) feedback(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "operator", "admin") {
		return
	}
	var feedback domain.Feedback
	if !decode(w, r, &feedback) {
		return
	}
	runID := r.PathValue("id")
	run, err := s.app.Repo.GetRun(r.Context(), runID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	feedback.ID = uuid.NewString()
	feedback.RunID = runID
	feedback.StoreID = run.Request.StoreID
	feedback.MemoryUpdates = nil
	feedback.CreatedAt = time.Now().UTC()
	if err := s.app.Memory.Validate(run, feedback); err != nil {
		respond(w, nil, err)
		return
	}
	// Persist the source event before materializing facts so every memory can
	// always be traced back to an existing feedback record.
	if err := s.app.Repo.AddFeedback(r.Context(), feedback); err != nil {
		respond(w, nil, err)
		return
	}
	updates, err := s.app.Memory.Learn(r.Context(), run, feedback)
	if err != nil {
		respond(w, nil, err)
		return
	}
	feedback.MemoryUpdates = updates
	if err := s.app.Repo.AddFeedback(r.Context(), feedback); err != nil {
		respond(w, nil, err)
		return
	}
	writeJSON(w, http.StatusCreated, feedback)
}

func (s *Server) getMerchantMemory(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "operator", "admin") {
		return
	}
	profile, err := s.app.Memory.Get(r.Context(), r.PathValue("id"))
	respond(w, profile, err)
}

func (s *Server) listPolicies(w http.ResponseWriter, r *http.Request) {
	state, err := s.app.Policies.State(r.Context())
	respond(w, state, err)
}

func (s *Server) listHarnessReports(w http.ResponseWriter, r *http.Request) {
	items, err := s.app.Repo.ListHarnessReports(r.Context())
	respond(w, items, err)
}

func (s *Server) listEvolutionRuns(w http.ResponseWriter, r *http.Request) {
	items, err := s.app.Repo.ListEvolutionRuns(r.Context())
	respond(w, items, err)
}

func (s *Server) runEvolution(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	var request struct {
		CanaryPercent int `json:"canary_percent"`
	}
	if !decode(w, r, &request) {
		return
	}
	run, err := s.app.Evolution.Evolve(r.Context(), request.CanaryPercent)
	respond(w, run, err)
}

func (s *Server) generateCandidate(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	candidate, err := s.app.Evolution.GenerateCandidate(r.Context())
	if err != nil {
		respond(w, nil, err)
		return
	}
	writeJSON(w, http.StatusCreated, candidate)
}

func (s *Server) evaluate(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	result, err := s.app.Evolution.Evaluate(r.Context(), r.PathValue("version"))
	respond(w, result, err)
}

func (s *Server) canary(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	var request struct {
		Percent int `json:"percent"`
	}
	if !decode(w, r, &request) {
		return
	}
	err := s.app.Evolution.StartCanary(r.Context(), r.PathValue("version"), request.Percent)
	respond(w, map[string]any{"status": "canary", "version": r.PathValue("version"), "percent": request.Percent}, err)
}

func (s *Server) promote(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	err := s.app.Evolution.Promote(r.Context(), r.PathValue("version"))
	respond(w, map[string]any{"status": "active", "version": r.PathValue("version")}, err)
}

func (s *Server) rollback(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	err := s.app.Evolution.Rollback(r.Context())
	respond(w, map[string]string{"status": "rolled_back"}, err)
}

func respond(w http.ResponseWriter, value any, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, value)
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeError(w, http.StatusBadRequest, err)
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func sse(w http.ResponseWriter, event string, value any) {
	payload, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("panic recovered", "error", recovered, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func actor(r *http.Request) string {
	if value := r.Header.Get("X-EvoOps-Actor"); value != "" {
		return value
	}
	return "anonymous"
}

func requireRole(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	role := r.Header.Get("X-EvoOps-Role")
	for _, candidate := range allowed {
		if role == candidate {
			return true
		}
	}
	writeError(w, http.StatusForbidden, fmt.Errorf("role %q is not permitted; allowed roles: %v", role, allowed))
	return false
}
