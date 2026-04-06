// main is the entry point for the AxiaOps API service.
// It reads ghost detection results from the database and serves them over HTTP.
// Ingestion is handled by a separate service (services/ingestion).
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"axiaops.io/api/internal/api"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/storage"
	"axiaops.io/shared/storage/postgres"
	"axiaops.io/shared/storage/sqlite"
)

// statusWriter captures the HTTP status code written by a handler.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.code = code
	sw.ResponseWriter.WriteHeader(code)
}

func main() {
	ctx := context.Background()

	// ── Storage ──────────────────────────────────────────────────────────────
	var store storage.Store
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		s, err := postgres.New(ctx, dbURL)
		if err != nil {
			log.Fatalf("storage: postgres init failed: %v", err)
		}
		defer s.Close()
		store = s
		log.Println("storage: using PostgreSQL")
	} else {
		dbPath := os.Getenv("DB_PATH")
		if dbPath == "" {
			dbPath = "axiaops.db"
		}
		s, err := sqlite.New(dbPath)
		if err != nil {
			log.Fatalf("storage: sqlite init failed: %v", err)
		}
		defer s.Close()
		store = s
		log.Println("storage: using SQLite")
	}

	// ── HTTP API ──────────────────────────────────────────────────────────────
	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	h := api.New(store)
	h.Register(mux)

	// ── Auth ──────────────────────────────────────────────────────────────────
	var root http.Handler = h.Handler(mux)
	kindeIssuer := os.Getenv("KINDE_ISSUER")
	if kindeIssuer == "" {
		log.Println("auth: KINDE_ISSUER not set — running without authentication")
	} else {
		auth, err := middleware.NewAuth(ctx, kindeIssuer, store)
		if err != nil {
			log.Fatalf("auth: init failed: %v", err)
		}
		log.Printf("auth: JWT verification enabled (issuer: %s)", kindeIssuer)
		root = auth.Wrap(root)
	}

	// Request logger — outermost layer so every request is visible.
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &statusWriter{ResponseWriter: w, code: 200}
		root.ServeHTTP(rw, r)
		log.Printf("%s %s → %d", r.Method, r.URL.Path, rw.code)
	})

	log.Printf("api: listening on %s", addr)
	if err := http.ListenAndServe(addr, logged); err != nil {
		log.Fatalf("api: server error: %v", err)
	}
}
