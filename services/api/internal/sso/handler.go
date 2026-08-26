package sso

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"axiaops.io/api/internal/httpjson"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/authz"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// Handler bundles the CRUD endpoints for sso_connections, sso_domains, and
// sso_group_mappings. The pre-auth GET /v1/sso/discover is in discover.go;
// here lives everything that goes through the auth + permission middleware.
//
// The Handler depends on the Discoverer + Connector seams (D11), not on the
// store directly. Domain CRUD goes through the store because the seam for
// domains is implicit — ListSSODomains/CreateSSODomain are pure data
// operations that don't differ between the self-hosted native impl and a
// future external-IdP-mirror impl. If a future Connector-style seam emerges
// for domains, it can be introduced without API churn.
type Handler struct {
	store      storage.Store
	connector  Connector
	discoverer Discoverer
	resolver   DNSResolver
	now        func() time.Time
}

// New returns a fully wired Handler.
func New(store storage.Store, connector Connector, discoverer Discoverer) *Handler {
	return &Handler{
		store:      store,
		connector:  connector,
		discoverer: discoverer,
		resolver:   DefaultDNSResolver,
		now:        time.Now,
	}
}

// SetDNSResolver overrides the resolver — test affordance.
func (h *Handler) SetDNSResolver(r DNSResolver) { h.resolver = r }

// SetClock overrides the clock — test affordance.
func (h *Handler) SetClock(now func() time.Time) { h.now = now }

// Register wires SSO routes onto the given mux. /v1/sso/discover is wired in
// the api Handler.Register because it bypasses auth (publicPath) — keeping
// all auth-bypass routes in one place avoids middleware accidents. This
// Register handles only the authenticated CRUD surface.
func (h *Handler) Register(mux *http.ServeMux) {
	require := func(p authz.Permission, fn http.HandlerFunc) http.Handler {
		return middleware.Require(p, h.store, http.HandlerFunc(fn))
	}

	// Connections.
	mux.Handle("GET /v1/sso/connections", require(authz.PermSSORead, h.listConnections))
	mux.Handle("GET /v1/sso/connections/{id}", require(authz.PermSSORead, h.getConnection))
	mux.Handle("POST /v1/sso/connections", require(authz.PermSSOManage, h.createConnection))
	mux.Handle("PATCH /v1/sso/connections/{id}", require(authz.PermSSOManage, h.updateConnection))
	mux.Handle("DELETE /v1/sso/connections/{id}", require(authz.PermSSOManage, h.deleteConnection))

	// Domains.
	mux.Handle("GET /v1/sso/domains", require(authz.PermSSORead, h.listDomains))
	mux.Handle("POST /v1/sso/domains", require(authz.PermSSODomainVerify, h.createDomain))
	mux.Handle("POST /v1/sso/domains/{id}/verify", require(authz.PermSSODomainVerify, h.verifyDomain))
	mux.Handle("DELETE /v1/sso/domains/{id}", require(authz.PermSSODomainVerify, h.deleteDomain))

	// Group mappings.
	mux.Handle("GET /v1/sso/connections/{id}/group-mappings", require(authz.PermSSORead, h.listGroupMappings))
	mux.Handle("PUT /v1/sso/connections/{id}/group-mappings", require(authz.PermSSOManage, h.replaceGroupMappings))
}

// ─── connections ────────────────────────────────────────────────────────────

func (h *Handler) listConnections(w http.ResponseWriter, r *http.Request) {
	ctx := withOrg(r)
	conns, err := h.connector.List(ctx)
	if err != nil {
		slog.Error("sso: list connections", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, conns)
}

func (h *Handler) getConnection(w http.ResponseWriter, r *http.Request) {
	ctx := withOrg(r)
	id := r.PathValue("id")
	c, err := h.connector.Get(ctx, id)
	if errors.Is(err, storage.ErrSSOConnectionNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		slog.Error("sso: get connection", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, c)
}

// connectionRequest is the shared shape for POST + PATCH bodies. Only fields
// included in the request are applied; absent fields don't overwrite
// existing values on PATCH.
type connectionRequest struct {
	Protocol            string `json:"protocol,omitempty"`
	Label               string `json:"label,omitempty"`
	Status              string `json:"status,omitempty"`
	Enforcement         string `json:"enforcement,omitempty"`
	DefaultRole         string `json:"default_role,omitempty"`
	IdPIssuer           string `json:"idp_issuer,omitempty"`
	IdPMetadataURL      string `json:"idp_metadata_url,omitempty"`
	IdPMetadataXML      string `json:"idp_metadata_xml,omitempty"`
	OIDCClientID        string `json:"oidc_client_id,omitempty"`
	OIDCDiscoveryURL    string `json:"oidc_discovery_url,omitempty"`
	OIDCTenantID        string `json:"oidc_tenant_id,omitempty"`
	// OIDCClientSecret is the plaintext on POST/PATCH; the handler encrypts
	// it before persisting. Never returned in responses (ciphertext is
	// `json:"-"` on the model).
	OIDCClientSecret string `json:"oidc_client_secret,omitempty"`
	// SAML fields — Phase C will wire these; included in the request shape
	// now so the API doesn't change between phases.
	SAMLSSOURL       string `json:"saml_sso_url,omitempty"`
	SAMLSigningCert  string `json:"saml_signing_cert,omitempty"`
	SAMLPreviousCert string `json:"saml_previous_cert,omitempty"`
	// ForceReauth is `*bool` rather than `bool` so the handler can
	// distinguish "field omitted from JSON" (apply default / no change)
	// from "explicitly set to false" (admin opting out). Default on create
	// is true; on PATCH a nil value is "no change".
	ForceReauth *bool `json:"force_reauth,omitempty"`
}

func (h *Handler) createConnection(w http.ResponseWriter, r *http.Request) {
	ctx := withOrg(r)
	var req connectionRequest
	if err := httpjson.Decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Encrypt the OIDC client_secret at the API boundary. The ciphertext
	// is what the postgres column stores; the runtime callback decrypts via
	// crypto.Decrypt(string(OIDCClientSecretCiphertext)) for the token
	// exchange. Same idiom as accounts.aws_secret_key_ciphertext.
	var oidcSecretCiphertext []byte
	if req.OIDCClientSecret != "" {
		ct, err := crypto.Encrypt(req.OIDCClientSecret)
		if err != nil {
			slog.Error("sso: create connection: encrypt client_secret", "error", err)
			writeError(w, http.StatusInternalServerError, "encryption failed")
			return
		}
		oidcSecretCiphertext = []byte(ct)
	}
	orgID := middleware.OrganizationID(r.Context())
	userID := middleware.UserID(r.Context())

	// force_reauth defaults to true on POST. Nil in the request means
	// "use the default"; explicit false means "admin opted out".
	forceReauth := true
	if req.ForceReauth != nil {
		forceReauth = *req.ForceReauth
	}

	c := model.SSOConnection{
		OrganizationID:   orgID,
		Protocol:         req.Protocol,
		Label:            req.Label,
		Status:           model.SSOStatusDraft, // POST always lands as draft
		Enforcement:      req.Enforcement,
		DefaultRole:      req.DefaultRole,
		IdPIssuer:        req.IdPIssuer,
		IdPMetadataURL:   req.IdPMetadataURL,
		IdPMetadataXML:   req.IdPMetadataXML,
		OIDCClientID:               req.OIDCClientID,
		OIDCClientSecretCiphertext: oidcSecretCiphertext,
		OIDCDiscoveryURL:           req.OIDCDiscoveryURL,
		OIDCTenantID:               req.OIDCTenantID,
		SAMLSSOURL:                 req.SAMLSSOURL,
		SAMLSigningCert:            req.SAMLSigningCert,
		SAMLPreviousCert:           req.SAMLPreviousCert,
		ForceReauth:                forceReauth,
		CreatedByUserID:            userID,
	}

	out, err := h.connector.Save(ctx, c)
	if err != nil {
		slog.Warn("sso: create connection", "error", err)
		writeError(w, http.StatusBadRequest, "create connection failed")
		return
	}
	writeJSONStatus(w, http.StatusCreated, out)
}

func (h *Handler) updateConnection(w http.ResponseWriter, r *http.Request) {
	ctx := withOrg(r)
	id := r.PathValue("id")

	existing, err := h.connector.Get(ctx, id)
	if errors.Is(err, storage.ErrSSOConnectionNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		slog.Error("sso: update connection get", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var req connectionRequest
	if err := httpjson.Decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Encrypt before applying — keeps the plaintext out of the in-memory
	// model copy except for this scope.
	if req.OIDCClientSecret != "" {
		ct, err := crypto.Encrypt(req.OIDCClientSecret)
		if err != nil {
			slog.Error("sso: update connection: encrypt client_secret", "error", err)
			writeError(w, http.StatusInternalServerError, "encryption failed")
			return
		}
		existing.OIDCClientSecretCiphertext = []byte(ct)
	}

	// Apply only fields explicitly set in the request. Empty string is
	// treated as "no change" — clearing a field requires a tombstone value
	// a future slice will define (e.g. "-" or a separate endpoint).
	if req.Label != "" {
		existing.Label = req.Label
	}
	if req.Status != "" {
		existing.Status = req.Status
	}
	if req.Enforcement != "" {
		existing.Enforcement = req.Enforcement
	}
	if req.DefaultRole != "" {
		existing.DefaultRole = req.DefaultRole
	}
	if req.IdPIssuer != "" {
		existing.IdPIssuer = req.IdPIssuer
	}
	if req.IdPMetadataURL != "" {
		existing.IdPMetadataURL = req.IdPMetadataURL
	}
	if req.IdPMetadataXML != "" {
		existing.IdPMetadataXML = req.IdPMetadataXML
	}
	if req.OIDCClientID != "" {
		existing.OIDCClientID = req.OIDCClientID
	}
	if req.OIDCDiscoveryURL != "" {
		existing.OIDCDiscoveryURL = req.OIDCDiscoveryURL
	}
	if req.OIDCTenantID != "" {
		existing.OIDCTenantID = req.OIDCTenantID
	}
	if req.SAMLSSOURL != "" {
		existing.SAMLSSOURL = req.SAMLSSOURL
	}
	if req.SAMLSigningCert != "" {
		existing.SAMLSigningCert = req.SAMLSigningCert
	}
	if req.SAMLPreviousCert != "" {
		existing.SAMLPreviousCert = req.SAMLPreviousCert
	}
	// force_reauth: nil = no change (preserve existing); explicit true/false
	// = apply. *bool lets the request distinguish "not in JSON" from
	// "explicit false".
	if req.ForceReauth != nil {
		existing.ForceReauth = *req.ForceReauth
	}

	out, err := h.connector.Save(ctx, existing)
	if err != nil {
		slog.Warn("sso: update connection", "error", err)
		writeError(w, http.StatusBadRequest, "update connection failed")
		return
	}
	writeJSON(w, out)
}

func (h *Handler) deleteConnection(w http.ResponseWriter, r *http.Request) {
	ctx := withOrg(r)
	id := r.PathValue("id")
	if err := h.connector.Delete(ctx, id); err != nil {
		if errors.Is(err, storage.ErrSSOConnectionNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		slog.Error("sso: delete connection", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── domains ────────────────────────────────────────────────────────────────

func (h *Handler) listDomains(w http.ResponseWriter, r *http.Request) {
	ctx := withOrg(r)
	domains, err := h.store.ListSSODomains(ctx)
	if err != nil {
		slog.Error("sso: list domains", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, domains)
}

type domainCreateRequest struct {
	SSOConnectionID string `json:"sso_connection_id"`
	Domain          string `json:"domain"`
}

func (h *Handler) createDomain(w http.ResponseWriter, r *http.Request) {
	ctx := withOrg(r)
	var req domainCreateRequest
	if err := httpjson.Decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	normalised, err := NormaliseDomain(req.Domain)
	if err != nil {
		// NormaliseDomain errors are user-facing input validation (public-suffix
		// rejection, missing label, etc.) — safe to surface verbatim.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.SSOConnectionID == "" {
		writeError(w, http.StatusBadRequest, "sso_connection_id required")
		return
	}

	d := model.SSODomain{
		OrganizationID:  middleware.OrganizationID(r.Context()),
		SSOConnectionID: req.SSOConnectionID,
		Domain:          normalised,
	}
	out, err := h.store.CreateSSODomain(ctx, d)
	if err != nil {
		slog.Warn("sso: create domain", "error", err)
		writeError(w, http.StatusBadRequest, "create domain failed")
		return
	}
	writeJSONStatus(w, http.StatusCreated, out)
}

// verifyDomain looks up TXT records on the domain and, if the verification
// token is present, advances status to verified with a 90d expiry.
//
// Verification can be re-run on a verified row to refresh expires_at — that's
// the operator's path back to verified after a stale sweep.
func (h *Handler) verifyDomain(w http.ResponseWriter, r *http.Request) {
	ctx := withOrg(r)
	id := r.PathValue("id")
	d, err := h.store.GetSSODomain(ctx, id)
	if errors.Is(err, storage.ErrSSODomainNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		slog.Error("sso: verify domain get", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	verifyCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ok, err := VerifyTXT(verifyCtx, h.resolver, d.Domain, d.VerificationToken)
	if err != nil {
		slog.Warn("sso: verify domain TXT lookup", "domain", d.Domain, "error", err)
		writeError(w, http.StatusBadGateway, "DNS lookup failed")
		return
	}
	if !ok {
		writeJSON(w, map[string]any{
			"verified": false,
			"reason":   "txt_record_not_found",
		})
		return
	}

	now := h.now().UTC()
	expiresAt := now.AddDate(0, 0, 90)
	if err := h.store.UpdateSSODomainStatus(ctx, d.ID, model.SSODomainStatusVerified, now, expiresAt); err != nil {
		slog.Error("sso: verify domain update", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, map[string]any{
		"verified":   true,
		"expires_at": expiresAt,
	})
}

func (h *Handler) deleteDomain(w http.ResponseWriter, r *http.Request) {
	ctx := withOrg(r)
	id := r.PathValue("id")
	if err := h.store.DeleteSSODomain(ctx, id); err != nil {
		if errors.Is(err, storage.ErrSSODomainNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		slog.Error("sso: delete domain", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── group mappings ─────────────────────────────────────────────────────────

func (h *Handler) listGroupMappings(w http.ResponseWriter, r *http.Request) {
	ctx := withOrg(r)
	connID := r.PathValue("id")
	mappings, err := h.store.ListSSOGroupMappings(ctx, connID)
	if err != nil {
		slog.Error("sso: list group mappings", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, mappings)
}

type groupMappingItem struct {
	GroupExternalID  string `json:"group_external_id"`
	GroupDisplayName string `json:"group_display_name"`
	Role             string `json:"role"`
}

type replaceGroupMappingsRequest struct {
	Mappings []groupMappingItem `json:"mappings"`
}

func (h *Handler) replaceGroupMappings(w http.ResponseWriter, r *http.Request) {
	ctx := withOrg(r)
	connID := r.PathValue("id")

	// Verify the connection exists in this org first; ListSSOGroupMappings
	// returning an empty list is ambiguous (no mappings vs. wrong connection),
	// so a Get up front gives the caller a clean 404 path. Goes through the
	// Connector seam so any impl that wraps Get for mirroring sees this read.
	if _, err := h.connector.Get(ctx, connID); err != nil {
		if errors.Is(err, storage.ErrSSOConnectionNotFound) {
			writeError(w, http.StatusNotFound, "connection not found")
			return
		}
		slog.Error("sso: replace group mappings get", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var req replaceGroupMappingsRequest
	if err := httpjson.Decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	orgID := middleware.OrganizationID(r.Context())
	mappings := make([]model.SSOGroupMapping, 0, len(req.Mappings))
	for _, m := range req.Mappings {
		mappings = append(mappings, model.SSOGroupMapping{
			OrganizationID:   orgID,
			SSOConnectionID:  connID,
			GroupExternalID:  m.GroupExternalID,
			GroupDisplayName: m.GroupDisplayName,
			Role:             m.Role,
		})
	}
	if err := h.store.ReplaceSSOGroupMappings(ctx, connID, mappings); err != nil {
		slog.Warn("sso: replace group mappings", "error", err)
		writeError(w, http.StatusBadRequest, "replace group mappings failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── helpers ────────────────────────────────────────────────────────────────

// withOrg sets the organization ID on the context for the storage layer's
// RLS scoping. Centralised here so every handler does it the same way.
func withOrg(r *http.Request) context.Context {
	return storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// writeJSONStatus is the explicit-status counterpart to writeJSON. Use it
// instead of `w.WriteHeader(status); writeJSON(w, v)` — that pattern flushes
// headers before writeJSON sets Content-Type, leaving the body without it.
// See `services/api/internal/api/handler.go:writeJSONStatus` for the full
// rationale (commit `bee01a2`).
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("sso: writeJSONStatus encode failed", "status", status, "err", err)
	}
}

// writeError writes a JSON error body with a top-level "error" string. Mirrors
// the convention used by api.handler for endpoints that don't use raw http.Error.
func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
