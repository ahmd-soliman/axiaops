// cmd/main.go — composition root for the self-hosted API binary.
//
// Wiring lives in services/api/internal/serverbuild (plan §4.8.3 / D11).
// This file is intentionally bootstrap-only: env reads, store/cache/queue
// init, license verify, signal handling, graceful shutdown. Handler
// registration, middleware composition, and ticker startup are
// ComposeServer's responsibility.
//
// SaaS reactivation will add cmd/api-saashosted/main.go alongside this
// file; that variant will swap a few constructors (a remote-IdP auth
// provider, a SaaS-specific Discoverer/Connector pair) and call the
// same ComposeServer.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/api/internal/serverbuild"
	"axiaops.io/api/internal/sso"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/httpauth"
	"axiaops.io/shared/license"
	"axiaops.io/shared/logging"
	"axiaops.io/shared/model"
	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
	"axiaops.io/shared/storage/postgres"
)

const stuckScanTimeout = 15 * time.Minute

// die logs a fatal error and exits with code 1.
func die(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

func main() {
	logging.Init("api")

	ctx := context.Background()

	// ── License (B1.6 amended + B1.7 layer 2) ───────────────────────────
	// Per docs/b1.6-amendment-feature-gating.md, VerifyAtBoot logs +
	// continues for the missing/expired cases — refuse-at-boot was retired
	// in favour of feature-gating at the scan path (license.IsScanAllowed).
	// The one case it still refuses is layer 2 anti-tamper (plan §4.10.2):
	// DEV_MODE=true on a host that has a license configured. That returns a
	// non-nil error and we die() loudly here.
	if err := license.VerifyAtBoot(devModeEnabled()); err != nil {
		die("license: refusing to start", "error", err.Error())
	}

	// ── Storage ──────────────────────────────────────────────────────────
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		die("storage: DATABASE_URL is required")
	}
	migrationURL := os.Getenv("MIGRATION_DATABASE_URL")
	if migrationURL == "" {
		migrationURL = dbURL
	}

	// Startup recovery: reset accounts left in "scanning" from a previous crash.
	if n, err := postgres.ResetStuckScans(ctx, migrationURL, stuckScanTimeout); err != nil {
		slog.Warn("startup: failed to reset stuck scans", "error", err)
	} else if n > 0 {
		slog.Warn("startup: reset stuck scanning accounts", "count", n)
	}

	s, err := postgres.NewWithOwner(ctx, dbURL, migrationURL)
	if err != nil {
		die("storage: postgres init failed", "error", err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			slog.Error("storage: close error", "error", err)
		}
	}()
	store := storage.Store(s)
	slog.Info("storage: using PostgreSQL")

	// ── Cache + Queue ────────────────────────────────────────────────────
	c := cache.New(os.Getenv("REDIS_URL"))
	defer func() { _ = c.Close() }()

	ingestionURL := os.Getenv("INGESTION_URL")
	if ingestionURL == "" {
		ingestionURL = "http://localhost:8081"
	}
	// Shared-secret HMAC for outbound api → ingestion calls (C-1, plan §3.3).
	// DEV_MODE allows empty; the receiving ingestion middleware is in
	// passthrough mode under the same posture. Single secret on the signer
	// side — rotation is verifier-only (ingestion holds current+next).
	ingestionSecret, hmacErr := httpauth.LoadFromEnv("INGESTION_SHARED_SECRET", devModeEnabled())
	if hmacErr != nil {
		die("hmac: " + hmacErr.Error())
	}
	q := queue.New(os.Getenv("REDIS_URL"), ingestionURL, ingestionSecret)
	defer func() { _ = q.Close() }()

	// ── Resolve modes from env ───────────────────────────────────────────
	devMode := devModeEnabled()
	nativeAuthActive := !devMode
	redisConfigured := os.Getenv("REDIS_URL") != ""

	// ── Dev-mode setup (must run BEFORE composing the server so the
	//    DevBypass middleware has a real org/user to inject) ─────────────
	devOrganizationID := ""
	devUserID := ""
	devUserEmail := ""
	if devMode {
		devOrganizationID = os.Getenv("DEV_ORGANIZATION_ID")
		if devOrganizationID == "" {
			die("auth: DEV_MODE=true requires DEV_ORGANIZATION_ID to be set")
		}
		devUserID = os.Getenv("DEV_USER_ID")
		if devUserID == "" {
			devUserID = "dev-user-axiaops"
		}
		devUserEmail = os.Getenv("DEV_USER_EMAIL")
		if devUserEmail == "" {
			devUserEmail = "dev@axiaops.local"
		}
		if err := store.EnsureOrganization(ctx, devOrganizationID, devOrganizationID, devOrganizationID); err != nil {
			die("auth: failed to ensure dev organization", "organization", devOrganizationID, "error", err)
		}
		if err := store.EnsureUser(ctx, model.User{
			ID:             devUserID,
			OrganizationID: devOrganizationID,
			Email:          devUserEmail,
			Name:           "Dev User",
		}); err != nil {
			die("auth: failed to ensure dev user", "user", devUserID, "error", err)
		}
		if err := store.EnsureDevMembership(ctx, devOrganizationID, devUserID, "owner"); err != nil {
			die("auth: failed to ensure dev membership", "user", devUserID, "error", err)
		}
		slog.Warn("auth: DEV_MODE — bypassing auth", "organization", devOrganizationID, "user", devUserID)
	}

	// ── First-owner install token (native-auth only, non-dev) ────────────
	if nativeAuthActive {
		if res, err := auth.MaybeGenerateInstallToken(ctx, store); err != nil {
			slog.Error("auth: install token generator failed", "err", err)
		} else {
			slog.Info("auth: install token state",
				"generated", res.Generated,
				"skipped", res.Skipped,
				"file", res.FilePath)
		}
	}

	// ── Build seam impls ─────────────────────────────────────────────────
	var authProvider auth.Provider
	var sessionMgr *auth.Manager
	cookieCfg := auth.NewCookieConfig()
	if !devMode {
		sessionMgr = buildSessionManager(store, c)
		authProvider = auth.NewNativeProvider(sessionMgr, membershipLookup(store))
		slog.Info("auth: native provider enabled (cookie + sessions table)")
	}

	ssoValidator := sso.NewValidator(c)
	ssoStateStore := sso.NewStateStore(c)
	ssoDiscoverer := sso.NewNativeDiscoverer(store, os.Getenv("PUBLIC_HOST"))
	ssoConnector := sso.NewNativeConnector(store)

	var enforcementResolver middleware.SSOEnforcementResolver
	if !devMode {
		enforcementResolver = middleware.NewStoreEnforcementResolver(store)
	}

	// ── Compose the HTTP handler ─────────────────────────────────────────
	metrics := serverbuild.NewDefaultMetrics()
	prometheus.MustRegister(metrics.RequestsTotal)
	prometheus.MustRegister(metrics.RequestDurationSeconds)

	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	// RATE_LIMIT_MAX overrides middleware.DefaultRateLimitMax. 0 / unset /
	// malformed → fall back to the default. Composition root parses here so
	// every binary (api / future api-saashosted) reads the same env name.
	rateLimitMax := 0
	if v := os.Getenv("RATE_LIMIT_MAX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rateLimitMax = n
		} else {
			slog.Warn("ratelimit: invalid RATE_LIMIT_MAX, using default", "value", v)
		}
	}

	cfg := serverbuild.Config{
		Addr:                 addr,
		PublicHost:           os.Getenv("PUBLIC_HOST"),
		DevMode:              devMode,
		DevOrganizationID:    devOrganizationID,
		DevUserID:            devUserID,
		DevUserEmail:         devUserEmail,
		RedisConfigured:      redisConfigured,
		RateLimitMax:         rateLimitMax,
		StuckScanTimeout:     stuckScanTimeout,
		MigrationDatabaseURL: migrationURL,
	}

	deps := serverbuild.Deps{
		Store:               store,
		Cache:               c,
		Queue:               q,
		IngestionSecret:     ingestionSecret,
		AuthProvider:        authProvider,
		Discoverer:          ssoDiscoverer,
		Connector:           ssoConnector,
		SessionManager:      sessionMgr,
		CookieConfig:        cookieCfg,
		EnforcementResolver: enforcementResolver,
		SSOValidator:        ssoValidator,
		SSOStateStore:       ssoStateStore,
		MetricsRegistry:     metrics,
	}

	handler, err := serverbuild.ComposeServer(cfg, deps)
	if err != nil {
		die("compose: failed", "error", err)
	}

	if redisConfigured {
		slog.Info("api: rate limiting enabled (60 req/min per organization)")
	} else if !devMode {
		slog.Warn("api: rate limiting disabled — REDIS_URL not set")
	}

	// ── HTTP server + graceful shutdown ──────────────────────────────────
	sigCtx, sigCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer sigCancel()

	// ── Tickers ──────────────────────────────────────────────────────────
	// Bound to sigCtx so SIGTERM/SIGINT stops them in the same beat as the
	// HTTP server drain. Mirrors the ingestion-side wiring.
	serverbuild.StartTickers(sigCtx, store, serverbuild.TickerOptions{
		MigrationDatabaseURL: migrationURL,
		StuckScanTimeout:     stuckScanTimeout,
		NativeAuthActive:     nativeAuthActive,
	})

	server := &http.Server{Addr: addr, Handler: handler}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api: listening", "addr", addr)
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			die("api: server error", "error", err)
		}
	case <-sigCtx.Done():
		slog.Warn("api: shutdown signal received, draining requests")
		shutdownStart := time.Now()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil && err != context.DeadlineExceeded {
			slog.Error("api: shutdown error", "error", err)
		}
		if err := s.Close(); err != nil {
			slog.Error("api: db close error", "error", err)
		}
		shutdownDuration := time.Since(shutdownStart).Seconds()
		slog.Info("api: shutdown complete", "duration_seconds", fmt.Sprintf("%.2f", shutdownDuration))
	}
}

// buildSessionManager wires the native-auth session orchestrator. Reads
// SESSION_TTL_HOURS and SESSIONS_PER_USER_CAP — defaults match
// docs/sso-implementation-plan.md §4.5 (24h TTL, cap 10).
func buildSessionManager(store storage.Store, c cache.Cache) *auth.Manager {
	cfg := auth.Config{
		TTL:             auth.DefaultSessionTTL,
		SessionsPerUser: auth.DefaultSessionsPerUserCap,
	}
	if v := os.Getenv("SESSION_TTL_HOURS"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h > 0 {
			cfg.TTL = time.Duration(h) * time.Hour
		} else {
			slog.Warn("auth: invalid SESSION_TTL_HOURS, using default", "value", v)
		}
	}
	if v := os.Getenv("SESSIONS_PER_USER_CAP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.SessionsPerUser = n
		} else {
			slog.Warn("auth: invalid SESSIONS_PER_USER_CAP, using default", "value", v)
		}
	}
	return auth.NewManager(store, auth.NewSessionCache(c), cfg)
}

func membershipLookup(store storage.Store) auth.MembershipLookup {
	return func(ctx context.Context, organizationID, userID string) (auth.MembershipDetails, error) {
		role, email, err := store.LookupMembership(ctx, organizationID, userID)
		if err != nil {
			return auth.MembershipDetails{}, err
		}
		return auth.MembershipDetails{Role: role, Email: email}, nil
	}
}
