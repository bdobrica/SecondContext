package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	cfg    config.Config
	logger *slog.Logger
	dbPool *pgxpool.Pool
	llm    llm.Client
}

type healthResponse struct {
	Status      string `json:"status"`
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Postgres    string `json:"postgres"`
	Timestamp   string `json:"timestamp"`
}

func NewServer(cfg config.Config, logger *slog.Logger, dbPool *pgxpool.Pool) *Server {
	return NewServerWithClient(cfg, logger, dbPool, llm.NewOpenAIClient(cfg.OpenAI))
}

func NewServerWithClient(cfg config.Config, logger *slog.Logger, dbPool *pgxpool.Pool, client llm.Client) *Server {
	return &Server{cfg: cfg, logger: logger, dbPool: dbPool, llm: client}
}

func (s *Server) Handler() http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(30 * time.Second))
	router.Use(s.loggingMiddleware)

	router.Get("/healthz", s.handleHealthz)
	router.Get("/v1/models", s.handleListModels)
	router.Post("/v1/responses", s.handleCreateResponse)
	router.Post("/memory/ingest", s.handleMemoryIngest)
	router.Get("/memory", s.handleListMemories)
	router.Delete("/memory/{memoryID}", s.handleDeleteMemory)

	return router
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	response := healthResponse{
		Status:      "ok",
		Service:     s.cfg.App.Name,
		Environment: s.cfg.App.Env,
		Postgres:    "disabled",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
	statusCode := http.StatusOK

	if s.dbPool != nil {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()

		if err := s.dbPool.Ping(ctx); err != nil {
			response.Status = "degraded"
			response.Postgres = "unavailable"
			statusCode = http.StatusServiceUnavailable
		} else {
			response.Postgres = "ok"
		}
	}

	writeJSON(w, statusCode, response)
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}
