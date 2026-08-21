// cmd/main.go — composition root for the API binary.
//
// Wiring lives in services/api/internal/serverbuild (plan §4.8.3 / D11).
// This file is intentionally bootstrap-only: env reads, store/cache/queue
// init, signal handling, graceful shutdown. Handler registration,
// middleware composition, and ticker startup are ComposeServer's
// responsibility.

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

// transactionalSMTPFromEnv reads the global SMTP relay config used as the invite
// mailer's fallback when an org has no email notification channel. In prod the
// values arrive from SSM exactly like DATABASE_URL. Empty SMTP_HOST ⇒ zero
// config ⇒ no global mailer (invite delivery then depends on a per-org channel).
// SMTP_PORT defaults to 587 (STARTTLS submission — Gmail SMTP relay, SES, etc.).
func transactionalSMTPFromEnv() model.EmailConfig {
	host := os.Getenv("SMTP_HOST")
	if host == "" {
		return model.EmailConfig{}
	}
	port := 587
	if v := os.Getenv("SMTP_PORT"); v != "" {
		// Fail fast on a typo'd port rather than silently using 587 — SMTP_HOST
		// is set, so the operator clearly intends email and a wrong/ignored port
		// would only surface as silent delivery failures later.
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			die("smtp: invalid SMTP_PORT (must be 1–65535)", "value", v)
		}
		port = n
	}
	return model.EmailConfig{
		SMTPHost: host,
		SMTPPort: port,
		SMTPUser: os.Getenv("SMTP_USER"),
		SMTPPass: os.Getenv("SMTP_PASS"),
		From:     os.Getenv("SMTP_FROM"),
		FromName: os.Getenv("SMTP_FROM_NAME"),
	}
}

func main() {
	logging.Init("api")

	ctx := context.Background()

	// ── Storage ──────────────────────────────────────────────────────────
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		die("storage: DATABASE_URL is required")
	}
	runtimeAdminURL := os.Getenv("RUNTIME_ADMIN_DATABASE_URL")
	if runtimeAdminURL == "" {
		// The bypass pool (adminPool) handles pre-auth / cross-org reads —
		// notably the native-login membership lookup (LookupUserByEmail reads
		// the RLS-protected `memberships` table with no app.organization_id set).
		// With no runtime-admin URL, NewWithRuntimeAdmin falls back to the
		// RLS-bound app pool, which silently returns zero rows there and breaks
		// native login for every user. DEV_MODE runs a single shared pool by
		// design; refuse to start in any other build rather than serve
		// silently-broken auth. The schema-owner connection
		// (MIGRATION_DATABASE_URL) is the migrate task's alone now — see
		// docs/runtime-admin-db-role.md.
		if !devModeEnabled() {
			die("storage: RUNTIME_ADMIN_DATABASE_URL is required outside DEV_MODE — without the RLS-bypass connection the pool falls back to the app pool and native login silently fails for all users")
		}
		runtimeAdminURL = dbURL
	}

	// Startup recovery: reset accounts left in "scanning" from a previous crash.
	// Cross-org maintenance — runs on the RLS-bypass connection.
	if n, err := postgres.ResetStuckScans(ctx, runtimeAdminURL, stuckScanTimeout); err != nil {
		slog.Warn("startup: failed to reset stuck scans", "error", err)
	} else if n > 0 {
		slog.Warn("startup: reset stuck scanning accounts", "count", n)
	}

	s, err := postgres.NewWithRuntimeAdmin(ctx, dbURL, runtimeAdminURL)
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
	devUserName := ""
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
		devUserName = os.Getenv("DEV_USER_NAME")
		if devUserName == "" {
			devUserName = "Dev User"
		}
		if err := store.EnsureOrganization(ctx, devOrganizationID, devOrganizationID, devOrganizationID); err != nil {
			die("auth: failed to ensure dev organization", "organization", devOrganizationID, "error", err)
		}
		if err := store.EnsureUser(ctx, model.User{
			ID:             devUserID,
			OrganizationID: devOrganizationID,
			Email:          devUserEmail,
			Name:           devUserName,
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
	// malformed → fall back to the default.
	rateLimitMax := 0
	if v := os.Getenv("RATE_LIMIT_MAX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rateLimitMax = n
		} else {
			slog.Warn("ratelimit: invalid RATE_LIMIT_MAX, using default", "value", v)
		}
	}

	cfg := serverbuild.Config{
		Addr:              addr,
		PublicHost:        os.Getenv("PUBLIC_HOST"),
		DevMode:           devMode,
		DevOrganizationID: devOrganizationID,
		DevUserID:         devUserID,
		DevUserEmail:      devUserEmail,
		DevUserName:       devUserName,
		RedisConfigured:   redisConfigured,
		RateLimitMax:      rateLimitMax,
		StuckScanTimeout:  stuckScanTimeout,
		TransactionalSMTP: transactionalSMTPFromEnv(),
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
		RuntimeAdminDatabaseURL: runtimeAdminURL,
		StuckScanTimeout:        stuckScanTimeout,
		NativeAuthActive:        nativeAuthActive,
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
		role, email, name, err := store.LookupMembership(ctx, organizationID, userID)
		if err != nil {
			return auth.MembershipDetails{}, err
		}
		return auth.MembershipDetails{Role: role, Email: email, Name: name}, nil
	}
}
