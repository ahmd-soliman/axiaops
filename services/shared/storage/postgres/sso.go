package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// connectionColumns is the canonical SELECT clause for sso_connections rows.
// Keeping it as a constant keeps every read path in sync with the scan.
const connectionColumns = `
	id, organization_id,
	protocol, label, status, enforcement, default_role,
	idp_issuer, idp_metadata_url, idp_metadata_xml,
	oidc_client_id, oidc_client_secret_ciphertext, oidc_discovery_url, oidc_tenant_id,
	saml_sso_url, saml_signing_cert, saml_previous_cert, saml_previous_cert_expires_at,
	kinde_connection_id, scim_token_ciphertext, scim_endpoint,
	COALESCE(created_by_user_id, ''), created_at, updated_at`

// scanSSOConnection consumes a row in connectionColumns order.
func scanSSOConnection(r rowScanner) (model.SSOConnection, error) {
	var c model.SSOConnection
	err := r.Scan(
		&c.ID, &c.OrganizationID,
		&c.Protocol, &c.Label, &c.Status, &c.Enforcement, &c.DefaultRole,
		&c.IdPIssuer, &c.IdPMetadataURL, &c.IdPMetadataXML,
		&c.OIDCClientID, &c.OIDCClientSecretCiphertext, &c.OIDCDiscoveryURL, &c.OIDCTenantID,
		&c.SAMLSSOURL, &c.SAMLSigningCert, &c.SAMLPreviousCert, &c.SAMLPreviousCertExpiresAt,
		&c.KindeConnectionID, &c.SCIMTokenCiphertext, &c.SCIMEndpoint,
		&c.CreatedByUserID, &c.CreatedAt, &c.UpdatedAt,
	)
	return c, err
}

// CreateSSOConnection persists a new draft connection. ID auto-assigned if empty.
func (s *Store) CreateSSOConnection(ctx context.Context, c model.SSOConnection) (model.SSOConnection, error) {
	if c.OrganizationID == "" {
		return model.SSOConnection{}, fmt.Errorf("postgres: create sso connection: organization_id required")
	}
	if c.Protocol != model.SSOProtocolOIDC && c.Protocol != model.SSOProtocolSAML {
		return model.SSOConnection{}, fmt.Errorf("postgres: create sso connection: invalid protocol %q", c.Protocol)
	}
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	if c.Status == "" {
		c.Status = model.SSOStatusDraft
	}
	if c.Enforcement == "" {
		c.Enforcement = model.SSOEnforcementOptional
	}
	if c.DefaultRole == "" {
		c.DefaultRole = "viewer"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.SSOConnection{}, fmt.Errorf("postgres: create sso connection begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return model.SSOConnection{}, err
	}

	var createdBy any
	if c.CreatedByUserID != "" {
		createdBy = c.CreatedByUserID
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO sso_connections (
			id, organization_id, protocol, label, status, enforcement, default_role,
			idp_issuer, idp_metadata_url, idp_metadata_xml,
			oidc_client_id, oidc_client_secret_ciphertext, oidc_discovery_url, oidc_tenant_id,
			saml_sso_url, saml_signing_cert, saml_previous_cert, saml_previous_cert_expires_at,
			kinde_connection_id, scim_token_ciphertext, scim_endpoint,
			created_by_user_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10,
			$11, $12, $13, $14,
			$15, $16, $17, $18,
			$19, $20, $21,
			$22
		)
		RETURNING `+connectionColumns,
		c.ID, c.OrganizationID, c.Protocol, c.Label, c.Status, c.Enforcement, c.DefaultRole,
		c.IdPIssuer, c.IdPMetadataURL, c.IdPMetadataXML,
		c.OIDCClientID, c.OIDCClientSecretCiphertext, c.OIDCDiscoveryURL, c.OIDCTenantID,
		c.SAMLSSOURL, c.SAMLSigningCert, c.SAMLPreviousCert, c.SAMLPreviousCertExpiresAt,
		c.KindeConnectionID, c.SCIMTokenCiphertext, c.SCIMEndpoint,
		createdBy,
	)
	out, err := scanSSOConnection(row)
	if err != nil {
		return model.SSOConnection{}, fmt.Errorf("postgres: create sso connection insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.SSOConnection{}, fmt.Errorf("postgres: create sso connection commit: %w", err)
	}
	return out, nil
}

// GetSSOConnection returns a connection in the request org.
func (s *Store) GetSSOConnection(ctx context.Context, id string) (model.SSOConnection, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.SSOConnection{}, fmt.Errorf("postgres: get sso connection begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return model.SSOConnection{}, err
	}

	row := tx.QueryRow(ctx, `SELECT `+connectionColumns+` FROM sso_connections WHERE id = $1`, id)
	c, err := scanSSOConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.SSOConnection{}, storage.ErrSSOConnectionNotFound
	}
	if err != nil {
		return model.SSOConnection{}, fmt.Errorf("postgres: get sso connection: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.SSOConnection{}, fmt.Errorf("postgres: get sso connection commit: %w", err)
	}
	return c, nil
}

// ListSSOConnections returns all connections in the request org, newest first.
func (s *Store) ListSSOConnections(ctx context.Context) ([]model.SSOConnection, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: list sso connections begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `SELECT `+connectionColumns+` FROM sso_connections ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list sso connections: %w", err)
	}
	defer rows.Close()

	var out []model.SSOConnection
	for rows.Next() {
		c, err := scanSSOConnection(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: list sso connections scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list sso connections iter: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list sso connections commit: %w", err)
	}
	return out, nil
}

// UpdateSSOConnection persists mutable fields. The DB CHECK constraint is the
// last line of defence on the active+OIDC → secret-required invariant.
func (s *Store) UpdateSSOConnection(ctx context.Context, c model.SSOConnection) error {
	if c.ID == "" {
		return fmt.Errorf("postgres: update sso connection: id required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update sso connection begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	tag, err := tx.Exec(ctx, `
		UPDATE sso_connections SET
			label = $2,
			status = $3,
			enforcement = $4,
			default_role = $5,
			idp_issuer = $6,
			idp_metadata_url = $7,
			idp_metadata_xml = $8,
			oidc_client_id = $9,
			oidc_client_secret_ciphertext = $10,
			oidc_discovery_url = $11,
			oidc_tenant_id = $12,
			saml_sso_url = $13,
			saml_signing_cert = $14,
			saml_previous_cert = $15,
			saml_previous_cert_expires_at = $16,
			updated_at = NOW()
		WHERE id = $1`,
		c.ID, c.Label, c.Status, c.Enforcement, c.DefaultRole,
		c.IdPIssuer, c.IdPMetadataURL, c.IdPMetadataXML,
		c.OIDCClientID, c.OIDCClientSecretCiphertext, c.OIDCDiscoveryURL, c.OIDCTenantID,
		c.SAMLSSOURL, c.SAMLSigningCert, c.SAMLPreviousCert, c.SAMLPreviousCertExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: update sso connection: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrSSOConnectionNotFound
	}
	return tx.Commit(ctx)
}

// DeleteSSOConnection removes a connection (CASCADE drops domains + mappings).
func (s *Store) DeleteSSOConnection(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete sso connection begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM sso_connections WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("postgres: delete sso connection: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrSSOConnectionNotFound
	}
	return tx.Commit(ctx)
}

// ─── sso_domains ────────────────────────────────────────────────────────────

const domainColumns = `
	id, organization_id, sso_connection_id, domain, status,
	verification_token, verified_at, last_asserted_at, expires_at,
	created_at, updated_at`

func scanSSODomain(r rowScanner) (model.SSODomain, error) {
	var d model.SSODomain
	err := r.Scan(
		&d.ID, &d.OrganizationID, &d.SSOConnectionID, &d.Domain, &d.Status,
		&d.VerificationToken, &d.VerifiedAt, &d.LastAssertedAt, &d.ExpiresAt,
		&d.CreatedAt, &d.UpdatedAt,
	)
	return d, err
}

// generateVerificationToken returns a 32-byte random hex string. The DB UNIQUE
// constraint backstops collisions; we still want high entropy so an attacker
// can't brute-force a domain claim.
func generateVerificationToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("verification token random: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// CreateSSODomain inserts a pending domain. Generates ID + verification_token if empty.
func (s *Store) CreateSSODomain(ctx context.Context, d model.SSODomain) (model.SSODomain, error) {
	if d.OrganizationID == "" {
		return model.SSODomain{}, fmt.Errorf("postgres: create sso domain: organization_id required")
	}
	if d.SSOConnectionID == "" {
		return model.SSODomain{}, fmt.Errorf("postgres: create sso domain: sso_connection_id required")
	}
	if d.Domain == "" {
		return model.SSODomain{}, fmt.Errorf("postgres: create sso domain: domain required")
	}
	if d.ID == "" {
		d.ID = uuid.New().String()
	}
	if d.VerificationToken == "" {
		tok, err := generateVerificationToken()
		if err != nil {
			return model.SSODomain{}, err
		}
		d.VerificationToken = tok
	}
	if d.Status == "" {
		d.Status = model.SSODomainStatusPending
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.SSODomain{}, fmt.Errorf("postgres: create sso domain begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return model.SSODomain{}, err
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO sso_domains (
			id, organization_id, sso_connection_id, domain, status, verification_token
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+domainColumns,
		d.ID, d.OrganizationID, d.SSOConnectionID, strings.ToLower(d.Domain),
		d.Status, d.VerificationToken,
	)
	out, err := scanSSODomain(row)
	if err != nil {
		return model.SSODomain{}, fmt.Errorf("postgres: create sso domain insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.SSODomain{}, fmt.Errorf("postgres: create sso domain commit: %w", err)
	}
	return out, nil
}

// GetSSODomain returns a single domain in the request org.
func (s *Store) GetSSODomain(ctx context.Context, id string) (model.SSODomain, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.SSODomain{}, fmt.Errorf("postgres: get sso domain begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return model.SSODomain{}, err
	}

	row := tx.QueryRow(ctx, `SELECT `+domainColumns+` FROM sso_domains WHERE id = $1`, id)
	d, err := scanSSODomain(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.SSODomain{}, storage.ErrSSODomainNotFound
	}
	if err != nil {
		return model.SSODomain{}, fmt.Errorf("postgres: get sso domain: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.SSODomain{}, fmt.Errorf("postgres: get sso domain commit: %w", err)
	}
	return d, nil
}

// GetVerifiedSSODomainByName looks up a verified row by lower(domain). Pre-auth
// — no organization context exists. Uses the admin pool to bypass RLS.
func (s *Store) GetVerifiedSSODomainByName(ctx context.Context, domain string) (model.SSODomain, error) {
	row := s.adminPool.QueryRow(ctx,
		`SELECT `+domainColumns+` FROM sso_domains
		 WHERE lower(domain) = lower($1) AND status = 'verified'
		 LIMIT 1`,
		domain,
	)
	d, err := scanSSODomain(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.SSODomain{}, storage.ErrSSODomainNotFound
	}
	if err != nil {
		return model.SSODomain{}, fmt.Errorf("postgres: get verified sso domain: %w", err)
	}
	return d, nil
}

// ListSSODomains returns all domains in the request org, newest first.
// VerificationToken is intentionally cleared on list rows — the token is the
// proof of domain ownership and is only returned on the create response. A
// viewer-tier user with sso:read should not be able to read tokens for
// domains pending verification (they could otherwise trigger a verification
// against a domain whose admin hasn't yet published the TXT record).
func (s *Store) ListSSODomains(ctx context.Context) ([]model.SSODomain, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: list sso domains begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `SELECT `+domainColumns+` FROM sso_domains ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list sso domains: %w", err)
	}
	defer rows.Close()
	var out []model.SSODomain
	for rows.Next() {
		d, err := scanSSODomain(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: list sso domains scan: %w", err)
		}
		d.VerificationToken = "" // never leak in list responses
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list sso domains iter: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list sso domains commit: %w", err)
	}
	return out, nil
}

// UpdateSSODomainStatus advances the lifecycle. verifiedAt + expiresAt are
// applied only when status='verified'; for other statuses the columns are
// NULLed (revoked/stale) or left unchanged (pending → no transitions in).
func (s *Store) UpdateSSODomainStatus(ctx context.Context, id, status string, verifiedAt, expiresAt time.Time) error {
	if id == "" {
		return fmt.Errorf("postgres: update sso domain status: id required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update sso domain status begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	var verifiedArg, expiresArg any
	if status == model.SSODomainStatusVerified {
		verifiedArg = verifiedAt
		expiresArg = expiresAt
	}

	tag, err := tx.Exec(ctx, `
		UPDATE sso_domains SET
			status = $2,
			verified_at = $3,
			expires_at = $4,
			updated_at = NOW(),
			last_asserted_at = CASE WHEN $2 = 'verified' THEN NOW() ELSE last_asserted_at END
		WHERE id = $1`,
		id, status, verifiedArg, expiresArg,
	)
	if err != nil {
		return fmt.Errorf("postgres: update sso domain status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrSSODomainNotFound
	}
	return tx.Commit(ctx)
}

// DeleteSSODomain removes a domain row.
func (s *Store) DeleteSSODomain(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete sso domain begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM sso_domains WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("postgres: delete sso domain: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrSSODomainNotFound
	}
	return tx.Commit(ctx)
}

// SweepStaleSSODomains marks expired verified rows as stale. Cross-org sweep —
// uses the admin pool. Idempotent.
func (s *Store) SweepStaleSSODomains(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.adminPool.Exec(ctx, `
		UPDATE sso_domains SET status = 'stale', updated_at = NOW()
		WHERE status = 'verified' AND expires_at IS NOT NULL AND expires_at <= $1`,
		now,
	)
	if err != nil {
		return 0, fmt.Errorf("postgres: sweep stale sso domains: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ─── sso_group_mappings ─────────────────────────────────────────────────────

// ListSSOGroupMappings returns mappings for a connection in the request org.
func (s *Store) ListSSOGroupMappings(ctx context.Context, connectionID string) ([]model.SSOGroupMapping, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: list sso group mappings begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT id, organization_id, sso_connection_id, group_external_id, group_display_name, role, created_at
		FROM sso_group_mappings
		WHERE sso_connection_id = $1
		ORDER BY group_display_name, group_external_id`, connectionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list sso group mappings: %w", err)
	}
	defer rows.Close()

	var out []model.SSOGroupMapping
	for rows.Next() {
		var m model.SSOGroupMapping
		if err := rows.Scan(
			&m.ID, &m.OrganizationID, &m.SSOConnectionID,
			&m.GroupExternalID, &m.GroupDisplayName, &m.Role, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: list sso group mappings scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list sso group mappings iter: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list sso group mappings commit: %w", err)
	}
	return out, nil
}

// ReplaceSSOGroupMappings is the only mutation path: PUT semantics. Atomic.
func (s *Store) ReplaceSSOGroupMappings(ctx context.Context, connectionID string, mappings []model.SSOGroupMapping) error {
	if connectionID == "" {
		return fmt.Errorf("postgres: replace sso group mappings: connection_id required")
	}
	for i, m := range mappings {
		if m.SSOConnectionID != "" && m.SSOConnectionID != connectionID {
			return fmt.Errorf("postgres: replace sso group mappings: row %d sso_connection_id mismatch (got %q, want %q)", i, m.SSOConnectionID, connectionID)
		}
		if m.GroupExternalID == "" {
			return fmt.Errorf("postgres: replace sso group mappings: row %d group_external_id required", i)
		}
		if m.Role != "admin" && m.Role != "member" && m.Role != "viewer" {
			return fmt.Errorf("postgres: replace sso group mappings: row %d invalid role %q", i, m.Role)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: replace sso group mappings begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM sso_group_mappings WHERE sso_connection_id = $1`, connectionID); err != nil {
		return fmt.Errorf("postgres: replace sso group mappings delete: %w", err)
	}

	for _, m := range mappings {
		id := m.ID
		if id == "" {
			id = uuid.New().String()
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO sso_group_mappings (
				id, organization_id, sso_connection_id, group_external_id, group_display_name, role
			) VALUES ($1, $2, $3, $4, $5, $6)`,
			id, m.OrganizationID, connectionID, m.GroupExternalID, m.GroupDisplayName, m.Role,
		); err != nil {
			return fmt.Errorf("postgres: replace sso group mappings insert: %w", err)
		}
	}
	return tx.Commit(ctx)
}
