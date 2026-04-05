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
	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/storage/sqlite"
)

func main() {
	ctx := context.Background()

	// ── Storage ──────────────────────────────────────────────────────────────
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "axiaops.db"
	}
	store, err := sqlite.New(dbPath)
	if err != nil {
		log.Fatalf("storage: init failed: %v", err)
	}
	defer store.Close()

	// ── Load ghosts from DB ───────────────────────────────────────────────────
	ghosts, err := store.LoadGhosts(ctx)
	if err != nil {
		log.Fatalf("storage: load ghosts: %v", err)
	}
	summary := analyzer.Summarize(ghosts)
	log.Printf("api: loaded %d ghost records from database", len(ghosts))

	// ── HTTP API ──────────────────────────────────────────────────────────────
	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	h := api.New(ghosts, summary, nil)
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

	log.Printf("api: listening on %s  →  GET /ghosts  GET /summary  GET /health", addr)
	if err := http.ListenAndServe(addr, root); err != nil {
		log.Fatalf("api: server error: %v", err)
	}
}
