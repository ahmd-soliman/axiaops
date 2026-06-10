package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/httpjson"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/authz"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/entitlement"
	"axiaops.io/shared/license"
	"axiaops.io/shared/model"
	"axiaops.io/shared/notifications"
	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
)

// Handler serves zombie detection results over HTTP.
type Handler struct {
	store        storage.Store
	queue        queue.Queue
	ingestionURL string // used only by sync queue fallback

	// ingestionSecret signs outbound calls to ingestion (POST
	// /v1/credentials/verify is the api-side hop; the sync queue path also
	// signs via syncqueue.New). nil ⇒ DEV_MODE; both ends fall back to
	// no-sign. See docs/c1-hmac-plan.md §3.3.
	ingestionSecret []byte

	// redisCache is the cache backend the readyz check pings. nil means
	// "Redis was not configured for this deployment" — readyz reports
	// "skipped" rather than treating it as a fault. Callers wire this only
	// when REDIS_URL is set; the wider request path uses cache.Cache directly
	// via middleware (rate limiter), independent of this field.
	redisCache cache.Cache

	// publicHost is the externally-reachable origin used to build
	// invitation redemption URLs (https://<host>/accept-invite?token=…).
	// Defaults to the empty string; the handler falls back to building
	// a relative URL when unset.
	publicHost string

	// channelTransports maps a notification channel kind (model.ChannelKind*)
	// to its Transport, used by POST /v1/channels/{id}/test. nil ⇒ /test
	// returns 500 (transports not wired — a server misconfiguration, since
	// serverbuild always wires them). Set via WithNotificationTransports;
	// tests inject fakes through the same seam.
	channelTransports map[string]notifications.Transport

	// inviteMailer delivers invitation redemption URLs to invitees on POST
	// /v1/invitations. nil ⇒ no delivery attempt (EmailDelivery omitted) — the
	// composition seam a SaaS reactivation swaps for a platform mailer. Set via
	// WithInviteMailer; serverbuild wires the default channel-first/global-SMTP
	// impl.
	inviteMailer InviteMailer

	// entitlementResolver gates the scan endpoint on per-tenant billing
	// entitlement instead of the license JWT. nil ⇒ license gate (self-hosted,
	// the default). Set only by cmd/api-saashosted via WithEntitlementResolver,
	// which also flips license.SetEnforcementBypass at boot. entitlementGrace is
	// the past_due window applied past current_period_end. See
	// docs/saas-platform-admin-design.md §7.1.
	entitlementResolver entitlement.Resolver
	entitlementGrace    time.Duration
}

// New creates a Handler backed by the given store and queue.
func New(store storage.Store, q queue.Queue) *Handler {
	ingestionURL := os.Getenv("INGESTION_URL")
	if ingestionURL == "" {
		ingestionURL = "http://localhost:8081"
	}
	return &Handler{store: store, queue: q, ingestionURL: ingestionURL}
}

// WithRedisCache wires the Redis-backed cache into the readyz check. Optional —
// if not called, readyz reports the Redis status as "skipped". Returns the
// receiver for fluent setup so main.go can chain `api.New(...).WithRedisCache(c)`.
func (h *Handler) WithRedisCache(c cache.Cache) *Handler {
	h.redisCache = c
	return h
}

// WithPublicHost sets the externally-reachable origin (https://<host>) used
// to build OOB redemption URLs for invitations and password resets. Empty →
// relative URLs the frontend resolves against window.location.origin.
func (h *Handler) WithPublicHost(publicHost string) *Handler {
	h.publicHost = strings.TrimRight(publicHost, "/")
	return h
}

// WithIngestionURL overrides the ingestion service URL. The default is read
// from INGESTION_URL (falling back to http://localhost:8081). Tests use this
// to point the role-verify call at an httptest.Server.
func (h *Handler) WithIngestionURL(url string) *Handler {
	h.ingestionURL = url
	return h
}

// WithIngestionSecret installs the shared HMAC secret used to sign outbound
// api → ingestion calls. nil ⇒ DEV_MODE; both ends fall back to no-sign.
// See docs/c1-hmac-plan.md §3.3.
func (h *Handler) WithIngestionSecret(secret []byte) *Handler {
	h.ingestionSecret = secret
	return h
}

// WithInviteMailer wires the seam that emails invitation redemption URLs on
// POST /v1/invitations. Not called ⇒ no delivery attempt (EmailDelivery
// omitted). serverbuild wires the default channel-first/global-SMTP mailer;
// tests inject a fake.
func (h *Handler) WithInviteMailer(m InviteMailer) *Handler {
	h.inviteMailer = m
	return h
}

// WithEntitlementResolver switches the scan endpoint from the license gate to
// the per-tenant entitlement gate (SaaS). nil resolver is a no-op — the license
// gate stays in force (self-hosted). grace is the past_due window; a zero grace
// defaults to entitlement.DefaultGraceDays so a misconfigured root never
// collapses the grace to nothing. Wired only by cmd/api-saashosted via
// serverbuild; tests inject a stub resolver.
func (h *Handler) WithEntitlementResolver(r entitlement.Resolver, grace time.Duration) *Handler {
	h.entitlementResolver = r
	if grace <= 0 {
		grace = time.Duration(entitlement.DefaultGraceDays) * 24 * time.Hour
	}
	h.entitlementGrace = grace
	return h
}

// WithNotificationTransports wires the per-kind transports used by
// POST /v1/channels/{id}/test. Not called ⇒ /test reports 503. Tests inject
// fakes here to avoid real network/SMTP calls.
func (h *Handler) WithNotificationTransports(transports map[string]notifications.Transport) *Handler {
	h.channelTransports = transports
	return h
}

// Register attaches the routes to the given mux. Each non-public route is
// wrapped in middleware.Require, which 403s any caller whose role does not
// grant the listed permission. Public routes (health, livez, readyz, version,
// me) skip Require — version sits behind authn but every authenticated user
// should be able to read it; me lets users who have lost their membership
// observe that fact. DELETE /v1/memberships/{id} is intentionally unwrapped
// because the handler implements a self-leave bypass that Require can't
// express; the handler then enforces stricter perms when the target is
// someone else.
func (h *Handler) Register(mux *http.ServeMux) {
	require := func(p authz.Permission, fn http.HandlerFunc) http.Handler {
		return middleware.Require(p, h.store, http.HandlerFunc(fn))
	}

	// Public infra paths (auth-bypass list in Auth.Wrap / DevBypass).
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /livez", h.livez)
	mux.HandleFunc("GET /readyz", h.readyz)

	// Authenticated, no permission gate.
	mux.HandleFunc("GET /v1/version", h.getVersion)
	mux.HandleFunc("GET /v1/me", h.getMe)

	// Zombies / summary / costs / resources / trend.
	mux.Handle("GET /v1/zombies", require(authz.PermZombiesRead, h.listZombies))
	mux.Handle("GET /v1/summary", require(authz.PermZombiesRead, h.getSummary))
	mux.Handle("GET /v1/summary/by-account", require(authz.PermZombiesRead, h.getSummaryByAccount))
	mux.Handle("GET /v1/trend", require(authz.PermSnapshotsRead, h.getTrend))
	mux.Handle("GET /v1/trend/services", require(authz.PermSnapshotsRead, h.getTrendServices))
	mux.Handle("GET /v1/trend/resource-types", require(authz.PermSnapshotsRead, h.getTrendResourceTypes))
	mux.Handle("GET /v1/costs", require(authz.PermCostsRead, h.listCosts))
	mux.Handle("GET /v1/resources", require(authz.PermResourcesRead, h.listResources))

	// Accounts.
	mux.Handle("GET /v1/accounts", require(authz.PermAccountsRead, h.listAccounts))
	mux.Handle("GET /v1/accounts/{id}", require(authz.PermAccountsRead, h.getAccount))
	mux.Handle("POST /v1/accounts", require(authz.PermAccountsWrite, h.createAccount))
	mux.Handle("POST /v1/accounts/draft", require(authz.PermAccountsWrite, h.createDraftAccount))
	mux.Handle("PATCH /v1/accounts/{id}", require(authz.PermAccountsWrite, h.updateAccount))
	mux.Handle("DELETE /v1/accounts/{id}", require(authz.PermAccountsDelete, h.deleteAccount))
	mux.Handle("POST /v1/accounts/{id}/scan", require(authz.PermAccountsScan, h.scanAccount))

	// Notification channels (docs/notifications-plan.md).
	mux.Handle("GET /v1/channels", require(authz.PermChannelsRead, h.listChannels))
	mux.Handle("POST /v1/channels", require(authz.PermChannelsManage, h.createChannel))
	mux.Handle("PATCH /v1/channels/{id}", require(authz.PermChannelsManage, h.updateChannel))
	mux.Handle("DELETE /v1/channels/{id}", require(authz.PermChannelsManage, h.deleteChannel))
	mux.Handle("POST /v1/channels/{id}/test", require(authz.PermChannelsManage, h.testChannel))
	mux.Handle("GET /v1/channels/{id}/dispatches", require(authz.PermChannelsRead, h.listChannelDispatches))

	// Dismissals.
	mux.Handle("POST /v1/dismissals", require(authz.PermZombiesDismiss, h.createDismissal))
	mux.Handle("DELETE /v1/dismissals/{id}", require(authz.PermZombiesDismiss, h.revokeDismissal))
	mux.Handle("GET /v1/dismissals", require(authz.PermZombiesRead, h.listDismissals))

	// Audit trail.
	mux.Handle("GET /v1/audit", require(authz.PermAuditRead, h.listAuditEvents))

	// Memberships (RBAC Phase 1). The handler does an additional stricter-perm
	// check on PATCH/POST when the target role is admin, and DELETE bypasses
	// the gate entirely for self-leave (with last-owner guard still applied).
	mux.Handle("GET /v1/memberships", require(authz.PermMembersRead, h.listMemberships))
	mux.Handle("POST /v1/memberships", require(authz.PermMembersInvite, h.createMembership))
	mux.Handle("PATCH /v1/memberships/{id}/role", require(authz.PermMembersManageBasic, h.updateMembershipRole))
	mux.Handle("POST /v1/users/{id}/password-reset", require(authz.PermMembersManageBasic, h.issuePasswordReset))
	mux.HandleFunc("DELETE /v1/memberships/{id}", h.deleteMembership) // self-leave bypass — handler enforces
	mux.Handle("POST /v1/organizations/transfer-ownership", require(authz.PermOrganizationTransfer, h.transferOwnership))

	// Organization rename + onboarding completion (Phase 2).
	mux.Handle("PATCH /v1/organizations/me", require(authz.PermOrganizationUpdate, h.updateCurrentOrganization))
	mux.Handle("POST /v1/organizations/me/onboarding/complete", require(authz.PermOrganizationUpdate, h.completeOnboarding))

	// Email-based invitations (Phase 2). See docs/invitation-flow.md.
	// createInvitation / revokeInvitation apply additional stricter-perm checks
	// when the target role is admin (mirrors POST /v1/memberships).
	mux.Handle("POST /v1/invitations", require(authz.PermMembersInvite, h.createInvitation))
	mux.Handle("GET /v1/invitations", require(authz.PermMembersRead, h.listInvitations))
	mux.Handle("DELETE /v1/invitations/{id}", require(authz.PermMembersInvite, h.revokeInvitation))

	// Self-service display-name edit (issue #78). Authn-only — every
	// authenticated user can rename themselves; the user_id from the
	// session is the capability.
	mux.HandleFunc("PATCH /v1/users/me", h.updateCurrentUser)

	// GDPR — right to erasure (see docs/rbac-design.md §10).
	// /users/me is authn-only: any logged-in user can delete themselves
	// (subject to the sole-owner guard enforced by the store).
	mux.HandleFunc("DELETE /v1/users/me", h.deleteCurrentUser)
	mux.Handle("DELETE /v1/organizations/me", require(authz.PermOrganizationDelete, h.deleteCurrentOrganization))

	// GDPR — right of access / portability (Art. 15 + 20). Owner-only because
	// the export bundles audit_log + billing + accounts in a single download.
	mux.Handle("GET /v1/export", require(authz.PermDataExport, h.exportOrganizationData))
}

// cors wraps a handler with CORS headers.
//
// CORS_ORIGIN sets the allowed origin. Two shapes are supported:
//   - "*"  — wildcard, fine for unauthenticated APIs but INCOMPATIBLE
//     with credentialed requests (the browser drops responses
//     that combine `Allow-Origin: *` with `Allow-Credentials: true`).
//   - "<origin>" or "<origin>,<origin>,…" — comma-separated allowlist;
//     the request's Origin header is reflected back when it matches,
//     and `Access-Control-Allow-Credentials: true` is emitted so the
//     browser sends/accepts the native-auth session cookie.
//
// In local dev set `CORS_ORIGIN=http://localhost:5173`. In production
// the dashboard is same-origin (nginx serves both) and the value is
// effectively unused. Default stays `*` for back-compat with the
// pre-native-auth CORS posture; the absence of credentials in that
// mode keeps responses readable.
func cors(next http.Handler) http.Handler {
	rawOrigin := os.Getenv("CORS_ORIGIN")
	if rawOrigin == "" {
		rawOrigin = "*"
	}
	allowlist := strings.Split(rawOrigin, ",")
	for i := range allowlist {
		allowlist[i] = strings.TrimSpace(allowlist[i])
	}
	wildcard := len(allowlist) == 1 && allowlist[0] == "*"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqOrigin := r.Header.Get("Origin")
		switch {
		case wildcard:
			// Legacy posture — wildcard origin, no credentials. Native
			// auth requests from a different origin will land but the
			// session cookie will not round-trip; operators of native
			// auth must set CORS_ORIGIN to a concrete value.
			w.Header().Set("Access-Control-Allow-Origin", "*")
		case reqOrigin != "" && originAllowed(allowlist, reqOrigin):
			w.Header().Set("Access-Control-Allow-Origin", reqOrigin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed reports whether origin is in the allowlist (exact match).
// Pulled out so it can be unit-tested in isolation.
func originAllowed(allowlist []string, origin string) bool {
	for _, allowed := range allowlist {
		if allowed == origin {
			return true
		}
	}
	return false
}

// Handler wraps the given handler with CORS middleware.
func (h *Handler) Handler(next http.Handler) http.Handler {
	return cors(next)
}

// Optional query params:
//   - ?account_id=<id>          filter to a single account
//   - ?include_dismissed=true   include dismissed/snoozed zombies (default: excluded)
func (h *Handler) listZombies(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
	accountID := r.URL.Query().Get("account_id")
	includeDismissed := r.URL.Query().Get("include_dismissed") == "true"

	zombies, err := h.store.LoadZombies(ctx)
	if err != nil {
		slog.Error("listZombies: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Filter by internal_account_id if provided.
	if accountID != "" {
		filtered := make([]model.ZombieResource, 0, len(zombies))
		for _, z := range zombies {
			if z.InternalAccountID == accountID {
				filtered = append(filtered, z)
			}
		}
		zombies = filtered
	}

	// Enrich with dismissal state and optionally filter dismissed resources.
	zombies, err = h.enrichWithDismissals(ctx, zombies, accountID, includeDismissed)
	if err != nil {
		slog.Error("listZombies: enrich dismissals failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if zombies == nil {
		zombies = []model.ZombieResource{}
	}
	writeJSON(w, zombies)
}

// enrichWithDismissals loads active dismissals for the organization and either annotates
// or removes dismissed/snoozed zombies from the list.
func (h *Handler) enrichWithDismissals(ctx context.Context, zombies []model.ZombieResource, accountID string, includeDismissed bool) ([]model.ZombieResource, error) {
	dismissals, err := h.store.ListActiveDismissals(ctx, accountID)
	if err != nil {
		return nil, err
	}

	// Build fingerprint → dismissal lookup.
	lookup := make(map[string]model.DismissAction, len(dismissals))
	for _, d := range dismissals {
		lookup[dismissalKey(d)] = d
	}

	if len(lookup) == 0 {
		return zombies, nil
	}

	out := make([]model.ZombieResource, 0, len(zombies))
	for _, z := range zombies {
		d, dismissed := lookup[zombieKey(z)]
		if dismissed {
			if !includeDismissed {
				continue // omit from default response
			}
			// Annotate with dismissal info.
			z.DismissalID = &d.ID
			z.DismissAction = d.Action
			z.DismissReason = d.Reason
			z.DismissNote = d.Note
			z.SnoozedUntil = d.SnoozedUntil
		}
		out = append(out, z)
	}
	return out, nil
}

// zombieKey returns a stable fingerprint string for a ZombieResource.
func zombieKey(z model.ZombieResource) string {
	return z.InternalAccountID + "|" + z.Provider + "|" + z.Service + "|" + z.Region + "|" + z.ResourceID
}

// dismissalKey returns the same fingerprint for a DismissAction.
func dismissalKey(d model.DismissAction) string {
	return d.AccountID + "|" + d.Provider + "|" + d.Service + "|" + d.Region + "|" + d.ResourceID
}

// Optional query param: ?account_id=<id> to filter to a single account.
func (h *Handler) listResources(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
	accountID := r.URL.Query().Get("account_id")
	resources, err := h.store.LoadResources(ctx)
	if err != nil {
		slog.Error("listResources: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Filter by internal_account_id if provided
	if accountID != "" {
		filtered := make([]model.ResourceRecord, 0)
		for _, resource := range resources {
			if resource.InternalAccountID == accountID {
				filtered = append(filtered, resource)
			}
		}
		resources = filtered
	}

	if resources == nil {
		resources = []model.ResourceRecord{}
	}
	writeJSON(w, resources)
}

func (h *Handler) getSummary(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
	accountID := r.URL.Query().Get("account_id")
	zombies, err := h.store.LoadZombies(ctx)
	if err != nil {
		slog.Error("getSummary: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if accountID != "" {
		filtered := make([]model.ZombieResource, 0, len(zombies))
		for _, z := range zombies {
			if z.InternalAccountID == accountID {
				filtered = append(filtered, z)
			}
		}
		zombies = filtered
	}
	// Exclude dismissed/snoozed resources from the savings summary.
	zombies, err = h.enrichWithDismissals(ctx, zombies, accountID, false)
	if err != nil {
		slog.Error("getSummary: enrich dismissals failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, analyzer.Summarize(zombies))
}

// getSummaryByAccount returns per-account zombie aggregates for the organization.
// It mirrors getSummary's dismissal-exclusion pipeline exactly (LoadZombies →
// enrichWithDismissals with accountID="" = all org dismissals) so the two
// endpoints never diverge on which zombies count, then groups in-memory.
func (h *Handler) getSummaryByAccount(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
	zombies, err := h.store.LoadZombies(ctx)
	if err != nil {
		slog.Error("getSummaryByAccount: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Exclude dismissed/snoozed resources — identical to getSummary so the two
	// endpoints agree on which zombies count.
	zombies, err = h.enrichWithDismissals(ctx, zombies, "", false)
	if err != nil {
		slog.Error("getSummaryByAccount: enrich dismissals failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, analyzer.SummarizeByAccount(zombies))
}

// getTrend returns zombie snapshots for the organization, ordered oldest-first.
// Optional query params: ?account_id=<id>, ?service=<name>.
// When service is set, returns per-service data from zombie_snapshot_services.
func (h *Handler) getTrend(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
	accountID := r.URL.Query().Get("account_id")
	service := r.URL.Query().Get("service")
	resourceType := r.URL.Query().Get("resource_type")

	var snaps []model.ZombieSnapshot
	var err error

	if service != "" {
		snaps, err = h.store.ListSnapshotsByService(ctx, service, resourceType, accountID)
	} else {
		snaps, err = h.store.ListSnapshots(ctx, accountID)
	}
	if err != nil {
		slog.Error("getTrend: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if snaps == nil {
		snaps = []model.ZombieSnapshot{}
	}
	writeJSON(w, snaps)
}

// getTrendServices returns distinct services available in snapshot data.
func (h *Handler) getTrendServices(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
	services, err := h.store.ListTrendServices(ctx)
	if err != nil {
		slog.Error("getTrendServices: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if services == nil {
		services = []string{}
	}
	writeJSON(w, services)
}

// getTrendResourceTypes returns distinct resource types for a given service.
func (h *Handler) getTrendResourceTypes(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
	service := r.URL.Query().Get("service")
	if service == "" {
		writeJSON(w, []string{})
		return
	}
	types, err := h.store.ListTrendResourceTypes(ctx, service)
	if err != nil {
		slog.Error("getTrendResourceTypes: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if types == nil {
		types = []string{}
	}
	writeJSON(w, types)
}

// listCosts returns cost records for the organization, filtered by account, service, and time window.
// Optional query params: ?account_id=<id>, ?service=<name>, ?days=<int> (default 30).
// account_id can be either the internal AxiaOps account UUID or the AWS account ID.
func (h *Handler) listCosts(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))

	accountIDParam := r.URL.Query().Get("account_id")
	filter := storage.CostFilter{
		Service: r.URL.Query().Get("service"),
		Days:    days,
	}

	// Absolute calendar window. `since`/`until` are ISO dates (YYYY-MM-DD,
	// both inclusive); when present they override the trailing `days` window
	// so the dashboard's Custom… date picker selects a fixed range rather
	// than silently degrading to "last N days".
	if since := r.URL.Query().Get("since"); since != "" {
		if t, err := time.Parse("2006-01-02", since); err == nil {
			filter.Since = t
		}
	}
	if until := r.URL.Query().Get("until"); until != "" {
		if t, err := time.Parse("2006-01-02", until); err == nil {
			filter.Until = t
		}
	}

	// account_id parameter can be either the internal UUID or the AWS account ID.
	// Strategy:
	// 1. If it looks like a UUID, try to look it up as internal account ID first
	// 2. If found, use both internal_account_id and account_id for filtering
	// 3. If not found or looks like AWS ID, use as account_id filter
	if accountIDParam != "" {
		// Try to look it up as an internal account ID (UUID)
		account, err := h.store.GetAccount(ctx, accountIDParam)
		if err == nil {
			// Found by internal UUID
			filter.InternalAccountID = accountIDParam
			if account.AccountID != "" {
				filter.AWSAccountID = account.AccountID
			}
			slog.Info("listCosts: filtered by internal account UUID", "internal_id", accountIDParam, "aws_id", account.AccountID)
		} else {
			// Account not found - it could be:
			// 1. An internal UUID that hasn't been added to accounts table yet (newly created)
			// 2. An AWS account ID
			// Try both approaches:
			filter.InternalAccountID = accountIDParam // Try as internal UUID
			filter.AWSAccountID = accountIDParam      // Also try as AWS account ID
			slog.Info("listCosts: filtered by parameter (either internal UUID or AWS ID)", "account_id", accountIDParam)
		}
	}

	records, err := h.store.ListCostRecords(ctx, filter)
	if err != nil {
		slog.Error("listCosts: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if records == nil {
		records = []model.CostRecord{}
	}
	writeJSON(w, records)
}

// Pinger is satisfied by *postgres.Store (which embeds pgxpool.Pool).
type Pinger interface {
	Ping(ctx context.Context) error
}

// getVersion returns the build identifier for the API service plus the
// boot-time license summary. Reads APP_VERSION / APP_COMMIT_SHA / APP_ENV,
// falling back to "dev" / "local" / "development" so a vanilla `make
// start-dev` still produces a usable response. Auth required (sits under
// /v1/) so the dashboard footer + license banner only learn about the API
// after a user has logged in.
//
// The license sub-object is the source the slice-8 LicenseBanner reads:
// state == "valid" + days_remaining < 14 → soon-to-expire banner; state ==
// "in_grace" → grace banner with renewal contact. State "not_loaded" (DEV_MODE
// / SaaS binary / pre-VerifyAtBoot) renders nothing. Only `state` is emitted
// in the not-loaded branch — emitting empty/zero values for the other fields
// would force the dashboard into "is this `expires_at: ""` a real value or
// just absence?" branching.
//
// Anything beyond build identifier + license belongs in /metrics or a
// structured log line, not here.
func (h *Handler) getVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"service": "api",
		"version": getenvOr("APP_VERSION", "dev"),
		"commit":  getenvOr("APP_COMMIT_SHA", "local"),
		"env":     getenvOr("APP_ENV", "development"),
		"license": licenseSummary(),
	})
}

// licenseSummary builds the version-endpoint license sub-object from the
// boot-time *License snapshot. The atomic.Pointer is read once via Snapshot
// and every other field is computed against that same pointer — calling
// SnapshotState here would re-read the atomic, opening a TOCTOU window where
// state describes a different snapshot than expires_at / days_remaining.
//
// Returns just {state} when no license is loaded so the dashboard can branch
// on a single field rather than infer absence from zero values.
func licenseSummary() map[string]any {
	// SaaS (cmd/api-saashosted) bypasses the license at boot
	// (license.SetEnforcementBypass) and gates on entitlement instead — there is
	// no customer-facing license under SaaS (design §7.4). Collapse the sub-object
	// to {state:"managed"} so the dashboard hides the License banner/page rather
	// than rendering the (bypassed, possibly not_loaded) license state. The
	// dashboard's plan/usage view replaces it (#131). Post B1.7-layer-4,
	// self-hosted/DEV_MODE never set the bypass, so this branch is SaaS-only.
	if license.IsEnforcementBypassed() {
		return map[string]any{"state": "managed"}
	}
	snap := license.Snapshot()
	if snap == nil {
		return map[string]any{"state": license.StateNotLoaded.String()}
	}
	return map[string]any{
		"state":             license.CheckExpiry(snap).String(),
		"customer_id":       snap.CustomerID,
		"expires_at":        snap.ExpiresAt.UTC().Format(time.RFC3339),
		"days_remaining":    snap.DaysRemaining(),
		"max_organizations": snap.MaxOrganizations,
	}
}

// scanGateBody returns the 403 JSON body for the license-gated scan endpoint.
// Three distinct error codes so the dashboard can pick the right banner copy
// and operators can route alerts on the label without parsing the human-
// readable detail. The amendment doc lays out the rationale.
//
// Switch is exhaustive over the known blocking states (StateExpired,
// StateNotLoaded). The default case is the regression guard: any future
// blocking state (a hypothetical StateTrialExpired, StateRevoked) hits the
// default with a generic "license_inactive" body AND a slog.Warn so the
// next time a state is added, the operator sees an unhandled-state warning
// in logs that points back here. Without the warn, a copy-paste regression
// could ship the wrong body silently for years.
//
// Returned as a pre-built []byte (no marshalling per request) — this fires
// only on the gate-blocked path which is already a customer-visible error,
// but the path stays allocation-free regardless.
//
// Test pinning: handler_license_gate_test.go has one assertion per known
// state. Adding a state requires the new state's branch + a new test case;
// the default-case slog.Warn at runtime is the safety net for shipping
// without test coverage of the new state.
func scanGateBody(state license.State) []byte {
	switch state {
	case license.StateExpired:
		return []byte(`{"error":"license_expired","detail":"License past grace period — contact sales@axiaops.io to renew"}`)
	case license.StateNotLoaded:
		return []byte(`{"error":"license_not_loaded","detail":"No license installed — see https://axiaops.io/install for instructions"}`)
	case license.StateValid, license.StateInGrace:
		// Defensive: caller (scanAccount handler) should never reach here
		// because IsScanAllowedForState returns true for these states. If
		// it does, something has flipped the gate without updating this
		// switch — slog.Warn so the misalignment surfaces.
		slog.Warn("scanGateBody: called with allow-listed state", "state", state.String())
		return []byte(`{"error":"license_inactive","detail":"License is not active. See /v1/version for state."}`)
	default:
		slog.Warn("scanGateBody: unhandled license state", "state", state.String())
		return []byte(`{"error":"license_inactive","detail":"License is not active. See /v1/version for state."}`)
	}
}

func getenvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	if p, ok := h.store.(Pinger); ok {
		if err := p.Ping(r.Context()); err != nil {
			slog.Error("health: db ping failed", "error", err)
			http.Error(w, `{"status":"error","db":"unreachable"}`, http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// livez answers the liveness question: "is this process responsive enough to
// take traffic?" Always returns 200 unless the Go runtime is so wedged it
// cannot reply at all (in which case the request times out and the orchestrator
// kills the instance — that's the only failure mode for this endpoint).
//
// No DB ping, no Redis ping, no cross-service check. ECS Express / k8s should
// wire their *instance* health probe to this so a transient DB blip doesn't
// trigger pod restarts. Deep dependency checks belong in /readyz.
func (h *Handler) livez(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// readyz answers the readiness question: "should the load balancer route
// requests to me right now?" Pings PostgreSQL (required — we can't serve
// anything without it) and reports Redis status (informational — degraded but
// not blocking, since the cache/queue/rate-limiter all have in-memory
// fallbacks).
//
// Returns 503 only when PostgreSQL is unreachable. Redis being down keeps the
// status code at 200 with the body field set to "unreachable" — pulling the
// instance out of rotation for a degraded mode would be worse than the
// degradation itself. Monitoring can alert on the body field.
//
// Deliberately does NOT check ingestion. The API serves all read endpoints
// from PostgreSQL alone; ingestion-down means scans are delayed, not that
// requests should stop being routed here. Once ingestion moves to Lambda
// (Phase 4) "is ingestion reachable" stops being a meaningful question
// anyway — the answer is in CloudWatch metrics, not HTTP.
func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	// Cap the deep check so a slow Postgres or Redis can't tie up readyz
	// past what ECS Express's check timeout will tolerate.
	ctx, cancel := context.WithTimeout(r.Context(), 1500*time.Millisecond)
	defer cancel()

	body := map[string]string{
		"status": "ok",
		"db":     "ok",
		"redis":  "skipped",
	}
	overallOK := true

	if p, ok := h.store.(Pinger); ok {
		if err := p.Ping(ctx); err != nil {
			slog.Error("readyz: db ping failed", "error", err)
			body["db"] = "unreachable"
			body["status"] = "error"
			overallOK = false
		}
	}

	if h.redisCache != nil {
		body["redis"] = "ok"
		if err := h.redisCache.Ping(ctx); err != nil {
			slog.Warn("readyz: redis ping failed", "error", err)
			body["redis"] = "unreachable"
			// Status stays "ok" if DB is fine — degraded but serving.
			if overallOK {
				body["status"] = "degraded"
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if !overallOK {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(body)
}

// listAccounts returns connected accounts for the organization (secrets masked).
func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
	accounts, err := h.store.ListAccounts(ctx)
	if err != nil {
		slog.Error("listAccounts: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if accounts == nil {
		accounts = []model.Account{}
	}
	writeJSON(w, accounts)
}

func (h *Handler) getAccount(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
	account, err := h.store.GetAccount(ctx, r.PathValue("id"))
	if err != nil {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}
	writeJSON(w, account)
}

// createAccount saves a new cloud account with encrypted credentials. This
// endpoint is the access-key path. Role-based onboarding goes through
// POST /v1/accounts/draft → PATCH /v1/accounts/{id} (see account_role.go).
func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider    string `json:"provider"`
		AuthMethod  string `json:"auth_method"`
		Label       string `json:"label"`
		AccessKeyID string `json:"access_key_id"`
		SecretKey   string `json:"secret_key"`
		Region      string `json:"region"`
	}
	if err := httpjson.Decode(w, r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.AuthMethod == model.AuthMethodRole {
		http.Error(w, "role accounts must be created via POST /v1/accounts/draft", http.StatusBadRequest)
		return
	}
	if req.AccessKeyID == "" || req.SecretKey == "" {
		http.Error(w, "access_key_id and secret_key are required", http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		req.Provider = "aws"
	}
	if req.Region == "" {
		req.Region = "eu-central-1"
	}

	secretEncrypted, err := crypto.Encrypt(req.SecretKey)
	if err != nil {
		http.Error(w, "encryption failed", http.StatusInternalServerError)
		return
	}

	organizationID := middleware.OrganizationID(r.Context())
	account := model.Account{
		ID:                uuid.New().String(),
		OrganizationID:    organizationID,
		Provider:          req.Provider,
		Label:             req.Label,
		AuthMethod:        model.AuthMethodAccessKey,
		AccessKeyID:       req.AccessKeyID,
		SecretEncrypted:   secretEncrypted,
		Region:            req.Region,
		Status:            "connected",
		ScanIntervalHours: 24,
		CreatedAt:         time.Now().UTC(),
	}

	ctx := storage.WithOrganizationID(r.Context(), organizationID)
	if err := h.store.SaveAccount(ctx, account); err != nil {
		slog.Error("createAccount: save failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionAccountConnected,
		ResourceType: "account",
		ResourceID:   account.ID,
		Metadata: map[string]any{
			"provider": account.Provider,
			"label":    account.Label,
			"region":   account.Region,
		},
	})

	writeJSONStatus(w, http.StatusCreated, account)
}

// updateAccount edits the label, access_key_id, region, secret_key, and/or scan_interval_hours of an account.
// secret_key is only re-encrypted when a non-empty value is provided.
func (h *Handler) updateAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	organizationID := middleware.OrganizationID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), organizationID)

	existing, err := h.store.GetAccount(ctx, id)
	if err != nil {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	var req struct {
		Label             *string `json:"label"`
		AccessKeyID       *string `json:"access_key_id"`
		SecretKey         *string `json:"secret_key"`
		RoleARN           *string `json:"role_arn"`
		Region            *string `json:"region"`
		ScanIntervalHours *int    `json:"scan_interval_hours"`
	}
	if err := httpjson.Decode(w, r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Role verification: if role_arn is supplied on a role-based account,
	// run a synchronous AssumeRole probe via ingestion before persisting.
	// Done first so the rest of this handler operates on the post-verify
	// state (status flipped to connected, account_id resolved).
	if req.RoleARN != nil && existing.AuthMethod == model.AuthMethodRole {
		h.handleRoleVerification(w, r, ctx, &existing, *req.RoleARN)
		return
	}

	if req.Label != nil {
		existing.Label = *req.Label
	}
	if req.AccessKeyID != nil {
		existing.AccessKeyID = *req.AccessKeyID
	}
	if req.SecretKey != nil && *req.SecretKey != "" {
		encrypted, err := crypto.Encrypt(*req.SecretKey)
		if err != nil {
			http.Error(w, "encryption failed", http.StatusInternalServerError)
			return
		}
		existing.SecretEncrypted = encrypted
	}
	if req.Region != nil {
		existing.Region = *req.Region
	}
	if req.ScanIntervalHours != nil {
		if *req.ScanIntervalHours < 0 {
			http.Error(w, "scan_interval_hours must be >= 0", http.StatusBadRequest)
			return
		}
		existing.ScanIntervalHours = *req.ScanIntervalHours
	}

	if err := h.store.SaveAccount(ctx, existing); err != nil {
		slog.Error("updateAccount: save failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Record which field names were changed — not the values, since old/new
	// secret_key must never leak into the audit table. Note req != nil checks
	// are what the handler already used above to decide which columns to update.
	changed := make([]string, 0, 5)
	if req.Label != nil {
		changed = append(changed, "label")
	}
	if req.AccessKeyID != nil {
		changed = append(changed, "access_key_id")
	}
	if req.SecretKey != nil && *req.SecretKey != "" {
		changed = append(changed, "secret_key")
	}
	if req.Region != nil {
		changed = append(changed, "region")
	}
	if req.ScanIntervalHours != nil {
		changed = append(changed, "scan_interval_hours")
	}
	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionAccountUpdated,
		ResourceType: "account",
		ResourceID:   existing.ID,
		Metadata: map[string]any{
			"fields_changed": changed,
		},
	})

	writeJSON(w, existing)
}

// handleRoleVerification runs the synchronous verify round-trip against
// ingestion for a role-based account. On success: persist role_arn, the
// resolved AWS account number, and flip status to connected. On failure:
// keep the existing status (pending_role_setup for first-time, error for
// re-verify of a connected account) and write the structured reason into
// error_message so the dashboard can surface it.
func (h *Handler) handleRoleVerification(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	account *model.Account,
	roleARN string,
) {
	if roleARN == "" {
		http.Error(w, "role_arn is required", http.StatusBadRequest)
		return
	}

	out, err := h.verifyRoleViaIngestion(ctx, roleARN, account.ExternalID, account.Region, account.OrganizationID)
	if err != nil {
		slog.Error("updateAccount: verify ingestion call failed", "account_id", account.ID, "error", err)
		http.Error(w, "verification service unavailable", http.StatusBadGateway)
		return
	}

	if !out.OK {
		// Verification failed — persist the reason. Pre-existing connected
		// accounts move to error; brand-new drafts stay pending so the user
		// can fix the trust policy and click Verify again without re-running
		// the draft step.
		account.RoleARN = roleARN
		account.ErrorMessage = out.Code + ": " + out.Reason
		if account.Status == model.AccountStatusConnected {
			account.Status = model.AccountStatusError
		}
		if err := h.store.SaveAccount(ctx, *account); err != nil {
			slog.Error("updateAccount: save after verify failure", "account_id", account.ID, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		audit.Record(r, h.store, model.AuditEvent{
			Action:       model.AuditActionAccountRoleVerifyFailed,
			ResourceType: "account",
			ResourceID:   account.ID,
			Metadata: map[string]any{
				"reason": out.Reason,
			},
		})

		// Surface a structured 400 so the dashboard can render targeted help.
		// `out.Detail` is the raw AWS error string — it carries ARNs, account
		// IDs, and request IDs, none of which belong in the customer's browser.
		// Log it server-side and return only the structured {code, reason}.
		slog.Info("updateAccount: role verify failed",
			"account_id", account.ID,
			"code", out.Code,
			"reason", out.Reason,
			"aws_detail", out.Detail)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"code":   out.Code,
			"reason": out.Reason,
		})
		return
	}

	// Verified. Persist the AWS account number that ingestion resolved via
	// GetCallerIdentity so the costs screen's internal_account_id filter
	// works for role-based accounts the same way it does for access-key ones.
	account.RoleARN = roleARN
	account.AccountID = out.AccountID
	account.Status = model.AccountStatusConnected
	account.ErrorMessage = ""
	if err := h.store.SaveAccount(ctx, *account); err != nil {
		slog.Error("updateAccount: save after verify success", "account_id", account.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionAccountRoleVerified,
		ResourceType: "account",
		ResourceID:   account.ID,
		Metadata: map[string]any{
			"role_arn": roleARN,
		},
	})

	writeJSON(w, *account)
}

// deleteAccount removes a connected account.
func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
	if err := h.store.DeleteAccount(ctx, id); err != nil {
		slog.Error("deleteAccount: failed", "account_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionAccountDeleted,
		ResourceType: "account",
		ResourceID:   id,
	})

	w.WriteHeader(http.StatusNoContent)
}

// scanAccount triggers an ingestion run for the given account.
func (h *Handler) scanAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	organizationID := middleware.OrganizationID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), organizationID)

	// Scan gate (plan §4.9.2b, post-amendment + SaaS §7.1). The gate now needs
	// the organization, so it runs AFTER org-id resolution. gateAllowsScan picks
	// the license path (self-hosted, h.entitlementResolver==nil) or the
	// per-tenant entitlement path (SaaS) and returns the pre-built 403 body.
	//
	// Content-Type set BEFORE WriteHeader because once headers are flushed the
	// Header() map mutations are dropped — writeJSON's set-then-encode pattern
	// only works correctly for implicit-200 responses.
	if ok, body := gateAllowsScan(ctx, h.entitlementResolver, h.entitlementGrace, organizationID); !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(body)
		return
	}

	account, err := h.store.GetAccount(ctx, id)
	if err != nil {
		slog.Error("scanAccount: account not found", "account_id", id, "error", err)
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	// Drafts have no role_arn yet; running ingestion against them would either
	// fail immediately (for role accounts) or use empty static keys, then leave
	// the row stuck in 'scanning' until the 15-minute recovery sweep. Reject
	// here so the dashboard can route the user to "Finish connecting" instead.
	if account.Status == model.AccountStatusPendingRoleSetup {
		http.Error(w, "account onboarding is not finished — verify the role connection first", http.StatusConflict)
		return
	}

	ok, err := h.store.TryMarkAccountScanning(ctx, id)
	if err != nil {
		slog.Error("scanAccount: mark scanning failed", "account_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "scan already in progress", http.StatusConflict)
		return
	}

	job := queue.ScanJob{
		OrganizationID: account.OrganizationID,
		AccountID:      account.ID,
		EnqueuedAt:     time.Now().UTC(),
		RequestID:      middleware.RequestIDFromCtx(r.Context()),
	}
	if err := h.queue.Enqueue(ctx, job); err != nil {
		slog.Error("scan.enqueue_failed", "account_id", id, "error", err)
		_ = h.store.UpdateAccountStatus(ctx, id, "error")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("scan.enqueued", "account_id", id, "organization_id", organizationID)

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionScanTriggered,
		ResourceType: "account",
		ResourceID:   id,
		Metadata: map[string]any{
			"account_label": account.Label,
			"region":        account.Region,
			"on_demand":     true,
		},
	})

	writeJSON(w, map[string]string{"status": "scanning"})
}

// createDismissal handles POST /v1/dismissals.
// Body: { account_id, provider, service, region, resource_id, action, reason, note?, snooze_until? }
func (h *Handler) createDismissal(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))

	var req struct {
		AccountID    string     `json:"account_id"`
		Provider     string     `json:"provider"`
		Service      string     `json:"service"`
		Region       string     `json:"region"`
		ResourceID   string     `json:"resource_id"`
		Action       string     `json:"action"`
		Reason       string     `json:"reason"`
		Note         string     `json:"note"`
		SnoozedUntil *time.Time `json:"snooze_until"`
	}
	if err := httpjson.Decode(w, r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields.
	if req.AccountID == "" || req.Provider == "" || req.Service == "" ||
		req.Region == "" || req.ResourceID == "" {
		http.Error(w, "account_id, provider, service, region, resource_id are required", http.StatusBadRequest)
		return
	}
	if req.Action != model.DismissActionDismiss && req.Action != model.DismissActionSnooze {
		http.Error(w, "action must be 'dismiss' or 'snooze'", http.StatusBadRequest)
		return
	}
	if !model.ValidDismissReasons[req.Reason] {
		http.Error(w, "invalid reason code", http.StatusBadRequest)
		return
	}
	if req.Reason == model.DismissReasonOther && req.Note == "" {
		http.Error(w, "note is required when reason is 'other'", http.StatusBadRequest)
		return
	}
	if req.Action == model.DismissActionSnooze {
		if req.SnoozedUntil == nil {
			http.Error(w, "snooze_until is required for snooze action", http.StatusBadRequest)
			return
		}
		if !req.SnoozedUntil.After(time.Now()) {
			http.Error(w, "snooze_until must be in the future", http.StatusBadRequest)
			return
		}
		maxSnooze := time.Now().AddDate(0, 0, model.MaxSnoozeDays)
		if req.SnoozedUntil.After(maxSnooze) {
			http.Error(w, "snooze_until must be within 90 days", http.StatusBadRequest)
			return
		}
	}

	d := model.DismissAction{
		AccountID:    req.AccountID,
		Provider:     req.Provider,
		Service:      req.Service,
		Region:       req.Region,
		ResourceID:   req.ResourceID,
		Action:       req.Action,
		Reason:       req.Reason,
		Note:         req.Note,
		SnoozedUntil: req.SnoozedUntil,
		DismissedBy:  dismissActor(r.Context()),
	}

	id, err := h.store.DismissZombie(ctx, d)
	if err != nil {
		if errors.Is(err, storage.ErrAlreadyDismissed) {
			http.Error(w, "resource already has an active dismissal", http.StatusConflict)
			return
		}
		slog.Error("createDismissal: failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	d.ID = id

	action := model.AuditActionDismissZombie
	if d.Action == model.DismissActionSnooze {
		action = model.AuditActionSnoozeZombie
	}
	audit.Record(r, h.store, model.AuditEvent{
		Action:       action,
		ResourceType: "dismissal",
		ResourceID:   strconv.FormatInt(id, 10),
		Reason:       d.Reason,
		Metadata: map[string]any{
			"provider":      d.Provider,
			"service":       d.Service,
			"region":        d.Region,
			"resource_id":   d.ResourceID,
			"note":          d.Note,
			"snoozed_until": d.SnoozedUntil,
		},
	})

	writeJSONStatus(w, http.StatusCreated, d)
}

// revokeDismissal handles DELETE /v1/dismissals/{id}.
func (h *Handler) revokeDismissal(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))

	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid dismissal id", http.StatusBadRequest)
		return
	}

	revokedBy := dismissActor(r.Context())
	if err := h.store.RevokeDismissal(ctx, id, revokedBy); err != nil {
		slog.Error("revokeDismissal: failed", "id", id, "error", err)
		http.Error(w, "dismissal not found or already revoked", http.StatusNotFound)
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionRevokeDismissal,
		ResourceType: "dismissal",
		ResourceID:   strconv.FormatInt(id, 10),
	})

	w.WriteHeader(http.StatusNoContent)
}

// listDismissals handles GET /v1/dismissals.
// Optional query param: ?account_id=<id>
func (h *Handler) listDismissals(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
	accountID := r.URL.Query().Get("account_id")

	dismissals, err := h.store.ListActiveDismissals(ctx, accountID)
	if err != nil {
		slog.Error("listDismissals: failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if dismissals == nil {
		dismissals = []model.DismissAction{}
	}
	writeJSON(w, dismissals)
}

// listAuditEvents handles GET /v1/audit.
// Query params (all optional): user_id, resource_type, resource_id, action,
// since (RFC3339), until (RFC3339), limit (1..500, default 50), cursor
// (opaque token from a previous response's next_cursor).
// Response: { "events": [...], "next_cursor": "<base64>" | "" }.
func (h *Handler) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))

	q := r.URL.Query()
	filter := model.AuditFilter{
		UserID:       q.Get("user_id"),
		ResourceType: q.Get("resource_type"),
		ResourceID:   q.Get("resource_id"),
		Action:       q.Get("action"),
		Limit:        50,
	}
	if filter.Action != "" && !model.ValidAuditActions[filter.Action] {
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
	if raw := q.Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			http.Error(w, "since must be RFC3339 timestamp", http.StatusBadRequest)
			return
		}
		filter.Since = t
	}
	if raw := q.Get("until"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			http.Error(w, "until must be RFC3339 timestamp", http.StatusBadRequest)
			return
		}
		filter.Until = t
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			http.Error(w, "limit must be between 1 and 500", http.StatusBadRequest)
			return
		}
		filter.Limit = n
	}
	if raw := q.Get("cursor"); raw != "" {
		cur, err := decodeAuditCursor(raw)
		if err != nil {
			http.Error(w, "invalid cursor", http.StatusBadRequest)
			return
		}
		filter.Cursor = cur
	}

	events, err := h.store.AuditLogList(ctx, filter)
	if err != nil {
		slog.Error("listAuditEvents: failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := struct {
		Events     []model.AuditEvent `json:"events"`
		NextCursor string             `json:"next_cursor,omitempty"`
	}{Events: events}
	if len(events) == filter.Limit {
		// There may be more — encode the last row as the cursor. When the caller
		// reaches the true end they'll get a short page and next_cursor == "".
		last := events[len(events)-1]
		resp.NextCursor = encodeAuditCursor(model.AuditCursor{
			CreatedAt: last.CreatedAt,
			ID:        last.ID,
		})
	}
	if resp.Events == nil {
		resp.Events = []model.AuditEvent{}
	}
	writeJSON(w, resp)
}

// encodeAuditCursor / decodeAuditCursor turn an (created_at, id) pair into
// an opaque base64 JSON token. Treating it as opaque leaves room to change the
// pagination key in the future without breaking clients that round-trip it.
func encodeAuditCursor(c model.AuditCursor) string {
	b, err := json.Marshal(c)
	if err != nil {
		// Unreachable today — AuditCursor holds only a time.Time and an int64.
		// If a future change to the struct breaks this, a log entry is cheaper
		// than discovering the regression via silent "end of results" responses.
		slog.Error("audit: encode cursor failed", "error", err)
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeAuditCursor(s string) (model.AuditCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return model.AuditCursor{}, err
	}
	var c model.AuditCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return model.AuditCursor{}, err
	}
	return c, nil
}

// dismissActor returns the identifier stored in dismissed_by / revoked_by.
// Prefers the stable user id (immutable UUID) so the stored value doesn't
// drift when a user's email changes. Falls back to email when the user id
// is unavailable, and finally to organization id so rows are never written
// with "".
func dismissActor(ctx context.Context) string {
	if id := middleware.UserID(ctx); id != "" {
		return id
	}
	if email := middleware.UserEmail(ctx); email != "" {
		return email
	}
	return middleware.OrganizationID(ctx)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// writeJSONStatus is the explicit-status counterpart to writeJSON. Use it
// instead of `w.WriteHeader(status); writeJSON(w, v)` — that pattern flushes
// the response headers before writeJSON's `Header().Set("Content-Type", …)`
// runs, leaving 201/4xx/5xx responses with no Content-Type. The dashboard's
// `request()` then falls through to `res.text()` and downstream consumers
// see a stringified body. See commit `bee01a2` for the original surfacing
// of this footgun.
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers already flushed by WriteHeader, so http.Error can't change
		// the status code. Best we can do is log; the client will see a
		// truncated body and surface a parse error.
		slog.Error("writeJSONStatus encode failed", "status", status, "err", err)
	}
}
