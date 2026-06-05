// Command api-admin is the AxiaOps platform admin plane (control/admin plane of
// docs/saas-platform-admin-design.md §3). It is the FIRST real second
// composition root — a deliberately minimal sibling of cmd/main.go (the tenant
// API), wiring internal/staff via serverbuild.ComposeAdminServer.
//
// Operational posture (design §4.3): this binary is NOT internet-facing the way
// the tenant API is — run it behind VPN / SSO-only / security-group-restricted
// ingress. It shares the same RDS + image build as the tenant API but listens
// on its own address (ADMIN_API_ADDR, default :8090) and has NO DEV_MODE auth
// bypass — staff auth is always real because the plane is cross-tenant.
//
// Bootstrap: `api-admin seed-staff --email … --name … --password …` mints the
// first superadmin (mirrors the tenant install-token flow but simpler for a
// small internal user set). After that, superadmins mint further staff via
// POST /admin/staff.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"axiaops.io/api/internal/serverbuild"
	"axiaops.io/api/internal/staff"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/logging"
	"axiaops.io/shared/storage/postgres"
)

func die(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

func main() {
	logging.Init("api-admin")

	// Subcommands. `seed-staff` is the first-superadmin bootstrap; default
	// (no args) runs the HTTP server.
	if len(os.Args) > 1 && os.Args[1] == "seed-staff" {
		runSeedStaff(os.Args[2:])
		return
	}

	ctx := context.Background()
	store := openStore(ctx)
	defer closeStore(store)

	c := cache.New(os.Getenv("REDIS_URL"))
	defer func() { _ = c.Close() }()

	sessions := staff.NewSessionManager(c, staffSessionTTL())
	provider := staff.NewSessionProvider(store, sessions)

	handler, err := serverbuild.ComposeAdminServer(serverbuild.AdminConfig{
		Addr:                adminAddr(),
		StaffSessionTTL:     staffSessionTTL(),
		LoginRateLimitPerIP: 0, // limiter default
		CORSOrigin:          os.Getenv("ADMIN_CORS_ORIGIN"),
	}, serverbuild.AdminDeps{
		Store:         store,
		Cache:         c,
		StaffProvider: provider,
		StaffSessions: sessions,
	})
	if err != nil {
		die("admin: compose failed", "error", err)
	}

	addr := adminAddr()
	sigCtx, sigCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer sigCancel()

	server := &http.Server{Addr: addr, Handler: handler}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("admin: listening", "addr", addr)
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			die("admin: server error", "error", err)
		}
	case <-sigCtx.Done():
		slog.Warn("admin: shutdown signal received, draining requests")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && err != context.DeadlineExceeded {
			slog.Error("admin: shutdown error", "error", err)
		}
		slog.Info("admin: shutdown complete")
	}
}

// openStore opens the postgres store using the same env contract as the tenant
// API: DATABASE_URL (RLS app pool) + RUNTIME_ADMIN_DATABASE_URL (RLS-bypass).
// The admin plane reads system + cross-org tables exclusively on the bypass
// pool, so RUNTIME_ADMIN_DATABASE_URL is mandatory here (no DEV_MODE collapse).
func openStore(ctx context.Context) *postgres.Store {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		die("storage: DATABASE_URL is required")
	}
	runtimeAdminURL := os.Getenv("RUNTIME_ADMIN_DATABASE_URL")
	if runtimeAdminURL == "" {
		die("storage: RUNTIME_ADMIN_DATABASE_URL is required — the admin plane reads system + cross-org tables on the RLS-bypass pool")
	}
	s, err := postgres.NewWithRuntimeAdmin(ctx, dbURL, runtimeAdminURL)
	if err != nil {
		die("storage: postgres init failed", "error", err)
	}
	return s
}

func closeStore(s *postgres.Store) {
	if err := s.Close(); err != nil {
		slog.Error("storage: close error", "error", err)
	}
}

func adminAddr() string {
	if v := os.Getenv("ADMIN_API_ADDR"); v != "" {
		return v
	}
	return ":8090"
}

func staffSessionTTL() time.Duration {
	if v := os.Getenv("STAFF_SESSION_TTL_HOURS"); v != "" {
		var h int
		if _, err := fmt.Sscanf(v, "%d", &h); err == nil && h > 0 {
			return time.Duration(h) * time.Hour
		}
	}
	return 8 * time.Hour
}
