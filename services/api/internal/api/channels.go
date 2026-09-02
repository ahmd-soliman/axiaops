package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/httpjson"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/notifications"
	"axiaops.io/shared/storage"
)

// channelSecretMask is what a channel's secret config fields read back as. A
// PATCH that sends this value (or empty) for a secret field keeps the stored
// secret — only a genuinely new value re-encrypts. Mirrors the
// accounts.secret_encrypted UX.
const channelSecretMask = "***"

// channelKindSupported reports whether a kind has a transport in v1. The DB
// enum also allows jira (pre-provisioned for #113), but creating one
// is rejected until its transport ships.
func channelKindSupported(kind string) bool {
	return kind == model.ChannelKindEmail || kind == model.ChannelKindSlack || kind == model.ChannelKindTeams
}

// channelResponse is the client-facing shape. ConfigCiphertext never leaves the
// server; Config is the decrypted blob with secret fields masked.
type channelResponse struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Label       string            `json:"label"`
	Enabled     bool              `json:"enabled"`
	TriggerRule model.TriggerRule `json:"trigger_rule"`
	Config      map[string]any    `json:"config"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func (h *Handler) listChannels(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
	channels, err := h.store.ListNotificationChannels(ctx)
	if err != nil {
		slog.Error("listChannels: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp := make([]channelResponse, 0, len(channels))
	for _, ch := range channels {
		cr, err := channelToResponse(ch)
		if err != nil {
			slog.Error("listChannels: redact failed", "channel_id", ch.ID, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		resp = append(resp, cr)
	}
	writeJSON(w, resp)
}

func (h *Handler) createChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind        string             `json:"kind"`
		Label       string             `json:"label"`
		Enabled     bool               `json:"enabled"`
		TriggerRule *model.TriggerRule `json:"trigger_rule"`
		Config      json.RawMessage    `json:"config"`
	}
	if err := httpjson.Decode(w, r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !channelKindSupported(req.Kind) {
		http.Error(w, "unsupported channel kind (v1 supports email and slack)", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Label) == "" {
		http.Error(w, "label is required", http.StatusBadRequest)
		return
	}
	if len(req.Config) == 0 {
		http.Error(w, "config is required", http.StatusBadRequest)
		return
	}

	rule := defaultTriggerRule()
	if req.TriggerRule != nil {
		rule = *req.TriggerRule
	}
	if msg, ok := validateTriggerRule(rule); !ok {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	canonical, err := canonicalCreateConfig(req.Kind, req.Config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ciphertext, err := crypto.Encrypt(string(canonical))
	if err != nil {
		slog.Error("createChannel: encrypt failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC()
	organizationID := middleware.OrganizationID(r.Context())
	ch := model.NotificationChannel{
		ID:               uuid.New().String(),
		OrganizationID:   organizationID,
		Kind:             req.Kind,
		Label:            req.Label,
		Enabled:          req.Enabled, // default false — admin tests, then enables
		TriggerRule:      rule,
		ConfigCiphertext: ciphertext,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	ctx := storage.WithOrganizationID(r.Context(), organizationID)
	if err := h.store.SaveNotificationChannel(ctx, ch); err != nil {
		slog.Error("createChannel: save failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionChannelCreated,
		ResourceType: "notification_channel",
		ResourceID:   ch.ID,
		Metadata:     map[string]any{"kind": ch.Kind, "label": ch.Label},
	})

	resp, err := channelToResponse(ch)
	if err != nil {
		slog.Error("createChannel: redact failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSONStatus(w, http.StatusCreated, resp)
}

func (h *Handler) updateChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	organizationID := middleware.OrganizationID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), organizationID)

	existing, err := h.store.GetNotificationChannel(ctx, id)
	if errors.Is(err, storage.ErrChannelNotFound) {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("updateChannel: load failed", "channel_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var req struct {
		Label       *string            `json:"label"`
		Enabled     *bool              `json:"enabled"`
		TriggerRule *model.TriggerRule `json:"trigger_rule"`
		Config      json.RawMessage    `json:"config"`
		// Kind is intentionally absent — kind is immutable (config blob is shaped
		// for it); the storage upsert ignores it too.
	}
	if err := httpjson.Decode(w, r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	changed := make([]string, 0, 4)
	if req.Label != nil {
		if strings.TrimSpace(*req.Label) == "" {
			http.Error(w, "label cannot be empty", http.StatusBadRequest)
			return
		}
		existing.Label = *req.Label
		changed = append(changed, "label")
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
		changed = append(changed, "enabled")
	}
	if req.TriggerRule != nil {
		if msg, ok := validateTriggerRule(*req.TriggerRule); !ok {
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		existing.TriggerRule = *req.TriggerRule
		changed = append(changed, "trigger_rule")
	}
	if len(req.Config) > 0 {
		plaintext, err := crypto.Decrypt(existing.ConfigCiphertext)
		if err != nil {
			slog.Error("updateChannel: decrypt existing failed", "channel_id", id, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		canonical, err := canonicalMergedConfig(existing.Kind, plaintext, req.Config)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ciphertext, err := crypto.Encrypt(string(canonical))
		if err != nil {
			slog.Error("updateChannel: encrypt failed", "channel_id", id, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		existing.ConfigCiphertext = ciphertext
		changed = append(changed, "config")
	}

	if err := h.store.SaveNotificationChannel(ctx, existing); err != nil {
		slog.Error("updateChannel: save failed", "channel_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionChannelUpdated,
		ResourceType: "notification_channel",
		ResourceID:   existing.ID,
		Metadata:     map[string]any{"fields_changed": changed},
	})

	resp, err := channelToResponse(existing)
	if err != nil {
		slog.Error("updateChannel: redact failed", "channel_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, resp)
}

func (h *Handler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))

	// Read first so the audit row can carry kind/label — after the delete the
	// UUID is unresolvable, leaving an audit reviewer with no idea what was
	// removed. Also gives the 404 path.
	existing, err := h.store.GetNotificationChannel(ctx, id)
	if errors.Is(err, storage.ErrChannelNotFound) {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("deleteChannel: load failed", "channel_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.store.DeleteNotificationChannel(ctx, id); err != nil {
		slog.Error("deleteChannel: failed", "channel_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionChannelDeleted,
		ResourceType: "notification_channel",
		ResourceID:   id,
		Metadata:     map[string]any{"kind": existing.Kind, "label": existing.Label},
	})

	w.WriteHeader(http.StatusNoContent)
}

// testChannel sends a fixed synthetic digest to the channel's transport and
// records the attempt as a dispatch row. Returns 200 with {status, error} — the
// HTTP call succeeds even when delivery fails; the body carries the result so
// the dashboard can toast accordingly.
func (h *Handler) testChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	organizationID := middleware.OrganizationID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), organizationID)

	ch, err := h.store.GetNotificationChannel(ctx, id)
	if errors.Is(err, storage.ErrChannelNotFound) {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("testChannel: load failed", "channel_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if h.channelTransports == nil {
		// Server misconfiguration: serverbuild always wires transports, so this
		// is a "shouldn't happen" state, not a retryable outage. 500 (not 503) —
		// 503 trips the dashboard's global "service unavailable" redirect.
		slog.Error("testChannel: no transports configured")
		http.Error(w, "notifications not configured", http.StatusInternalServerError)
		return
	}
	transport, ok := h.channelTransports[ch.Kind]
	if !ok {
		http.Error(w, "no transport for channel kind "+ch.Kind, http.StatusBadRequest)
		return
	}

	payload := syntheticTestPayload(h.publicHost)
	sendCtx, cancel := context.WithTimeout(ctx, notifications.DefaultSendTimeout)
	defer cancel()
	now := time.Now().UTC()
	externalID, sendErr := transport.Send(sendCtx, ch, payload)

	rec := model.NotificationDispatch{
		OrganizationID: organizationID,
		ChannelID:      ch.ID,
		Source:         model.DispatchSourceTest,
		// SnapshotID + AccountID are intentionally empty: a /test send isn't
		// tied to a real scan, and a fake account_id would violate the FK
		// (or be NULLed). Leave them NULL.
		Status:              model.DispatchStatusSent,
		ZombieCount:         payload.ZombieCount,
		MonthlySavingsCents: 12345, // $123.45 synthetic
		Attempts:            1,
		ExternalTicketID:    externalID,
		DispatchedAt:        &now,
	}
	if sendErr != nil {
		rec.Status = model.DispatchStatusFailed
		rec.Error = sendErr.Error() // transport has already scrubbed any secret
	}
	if err := h.store.SaveNotificationDispatch(ctx, rec); err != nil {
		slog.Error("testChannel: record dispatch failed", "channel_id", id, "error", err)
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionChannelTested,
		ResourceType: "notification_channel",
		ResourceID:   ch.ID,
		Metadata:     map[string]any{"result": rec.Status},
	})

	out := map[string]any{"status": rec.Status}
	if rec.Error != "" {
		out["error"] = rec.Error
	}
	writeJSON(w, out)
}

func (h *Handler) listChannelDispatches(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))

	// Confirm the channel exists in this org first — gives a clean 404 and
	// keeps the dispatch list from being an org-scoped existence oracle.
	if _, err := h.store.GetNotificationChannel(ctx, id); errors.Is(err, storage.ErrChannelNotFound) {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	} else if err != nil {
		slog.Error("listChannelDispatches: load channel failed", "channel_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	limit := 0 // store applies its default + ceiling
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}

	dispatches, err := h.store.ListNotificationDispatches(ctx, id, limit)
	if err != nil {
		slog.Error("listChannelDispatches: load failed", "channel_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if dispatches == nil {
		dispatches = []model.NotificationDispatch{}
	}
	writeJSON(w, dispatches)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func defaultTriggerRule() model.TriggerRule {
	return model.TriggerRule{MinMonthlySavingsUSD: 25, DigestTopN: 10, On: []string{model.TriggerEventNewZombies}}
}

func validateTriggerRule(rule model.TriggerRule) (string, bool) {
	if rule.MinMonthlySavingsUSD < 0 {
		return "trigger_rule.min_monthly_savings_usd must be >= 0", false
	}
	if rule.DigestTopN < 0 {
		return "trigger_rule.digest_top_n must be >= 0", false
	}
	// Reject unknown events so a typo can't silently no-op now and start
	// suppressing the digest when the v2 On gate ships.
	for _, ev := range rule.On {
		if ev != model.TriggerEventNewZombies {
			return "trigger_rule.on supports only \"" + model.TriggerEventNewZombies + "\" in v1", false
		}
	}
	return "", true
}

func syntheticTestPayload(publicHost string) notifications.Payload {
	return notifications.Payload{
		AccountID:      "test-account",
		ZombieCount:    5,
		MonthlySavings: 123.45,
		Currency:       "USD",
		TopServices: []notifications.ServiceRow{
			{Service: "AmazonEC2", Count: 2, Savings: 80.00},
			{Service: "AmazonRDS", Count: 2, Savings: 30.45},
			{Service: "AWSLambda", Count: 1, Savings: 13.00},
		},
		DashboardURL: strings.TrimSuffix(publicHost, "/"),
	}
}

// channelToResponse decrypts the stored config and masks its secret fields.
func channelToResponse(ch model.NotificationChannel) (channelResponse, error) {
	cfg, err := redactConfig(ch)
	if err != nil {
		return channelResponse{}, err
	}
	return channelResponse{
		ID:          ch.ID,
		Kind:        ch.Kind,
		Label:       ch.Label,
		Enabled:     ch.Enabled,
		TriggerRule: ch.TriggerRule,
		Config:      cfg,
		CreatedAt:   ch.CreatedAt,
		UpdatedAt:   ch.UpdatedAt,
	}, nil
}

// webhookOnlyKinds are channel kinds whose entire config is a single
// webhook_url field (Slack, Teams). Handling them as one case here — and in
// canonicalCreateConfig/canonicalMergedConfig below — avoids a third
// near-identical switch case each time another webhook-shaped kind ships.
var webhookOnlyKinds = map[string]bool{
	model.ChannelKindSlack: true,
	model.ChannelKindTeams: true,
}

// webhookURLConfig is the JSON shape shared by SlackConfig and TeamsConfig
// (both are just {webhook_url}). Decoding into this instead of the named
// model type lets every webhook-only kind share one code path; the encoded
// bytes are identical to marshalling the named type, since the json tags match.
type webhookURLConfig struct {
	WebhookURL string `json:"webhook_url"`
}

func redactConfig(ch model.NotificationChannel) (map[string]any, error) {
	plaintext, err := crypto.Decrypt(ch.ConfigCiphertext)
	if err != nil {
		return nil, err
	}
	switch {
	case ch.Kind == model.ChannelKindEmail:
		var c model.EmailConfig
		if err := json.Unmarshal([]byte(plaintext), &c); err != nil {
			return nil, err
		}
		recipients := c.Recipients
		if recipients == nil {
			recipients = []string{}
		}
		return map[string]any{
			"smtp_host":  c.SMTPHost,
			"smtp_port":  c.SMTPPort,
			"smtp_user":  c.SMTPUser,
			"smtp_pass":  maskIfSet(c.SMTPPass),
			"from":       c.From,
			"from_name":  c.FromName,
			"recipients": recipients,
		}, nil
	case webhookOnlyKinds[ch.Kind]:
		var c webhookURLConfig
		if err := json.Unmarshal([]byte(plaintext), &c); err != nil {
			return nil, err
		}
		return map[string]any{"webhook_url": maskIfSet(c.WebhookURL)}, nil
	default:
		return map[string]any{}, nil
	}
}

func maskIfSet(s string) string {
	if s == "" {
		return ""
	}
	return channelSecretMask
}

// canonicalCreateConfig validates an incoming config for a new channel and
// returns its canonical JSON encoding (ready to encrypt). Validation errors are
// safe to surface to the client.
func canonicalCreateConfig(kind string, raw json.RawMessage) ([]byte, error) {
	switch {
	case kind == model.ChannelKindEmail:
		var c model.EmailConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("invalid email config: %w", err)
		}
		if err := validateEmailConfig(c); err != nil {
			return nil, err
		}
		return json.Marshal(c)
	case webhookOnlyKinds[kind]:
		var c webhookURLConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("invalid %s config: %w", kind, err)
		}
		if c.WebhookURL == "" {
			return nil, fmt.Errorf("%s config requires webhook_url", kind)
		}
		return json.Marshal(c)
	default:
		return nil, fmt.Errorf("unsupported channel kind %q", kind)
	}
}

// canonicalMergedConfig merges an incoming config onto the existing decrypted
// config, keeping a stored secret when the incoming secret field is empty or the
// "***" mask. Returns canonical JSON ready to re-encrypt.
func canonicalMergedConfig(kind, existingPlaintext string, raw json.RawMessage) ([]byte, error) {
	switch {
	case kind == model.ChannelKindEmail:
		var existing, incoming model.EmailConfig
		if err := json.Unmarshal([]byte(existingPlaintext), &existing); err != nil {
			return nil, fmt.Errorf("decode stored config: %w", err)
		}
		if err := json.Unmarshal(raw, &incoming); err != nil {
			return nil, fmt.Errorf("invalid email config: %w", err)
		}
		// Non-secret fields are taken from the (full) incoming object.
		existing.SMTPHost = incoming.SMTPHost
		existing.SMTPPort = incoming.SMTPPort
		existing.SMTPUser = incoming.SMTPUser
		existing.From = incoming.From
		existing.FromName = incoming.FromName
		existing.Recipients = incoming.Recipients
		// Secret: keep stored unless a genuinely new value was supplied.
		if incoming.SMTPPass != "" && incoming.SMTPPass != channelSecretMask {
			existing.SMTPPass = incoming.SMTPPass
		}
		if err := validateEmailConfig(existing); err != nil {
			return nil, err
		}
		return json.Marshal(existing)
	case webhookOnlyKinds[kind]:
		var existing, incoming webhookURLConfig
		if err := json.Unmarshal([]byte(existingPlaintext), &existing); err != nil {
			return nil, fmt.Errorf("decode stored config: %w", err)
		}
		if err := json.Unmarshal(raw, &incoming); err != nil {
			return nil, fmt.Errorf("invalid %s config: %w", kind, err)
		}
		if incoming.WebhookURL != "" && incoming.WebhookURL != channelSecretMask {
			existing.WebhookURL = incoming.WebhookURL
		}
		if existing.WebhookURL == "" {
			return nil, fmt.Errorf("%s config requires webhook_url", kind)
		}
		return json.Marshal(existing)
	default:
		return nil, fmt.Errorf("unsupported channel kind %q", kind)
	}
}

func validateEmailConfig(c model.EmailConfig) error {
	if c.SMTPHost == "" || c.SMTPPort == 0 || c.From == "" || len(c.Recipients) == 0 {
		return errors.New("email config requires smtp_host, smtp_port, from, and at least one recipient")
	}
	// Reject CR/LF in any address that lands in a mail header (From:/To:). The
	// transport interpolates these straight into the RFC 5322 headers, so a
	// newline would let an admin-supplied value inject extra headers. Config is
	// admin-only, so this is defence-in-depth, not a privilege boundary.
	if hasCRLF(c.From) {
		return errors.New("from must not contain newlines")
	}
	if hasCRLF(c.FromName) {
		return errors.New("from_name must not contain newlines")
	}
	for _, r := range c.Recipients {
		if hasCRLF(r) {
			return errors.New("recipient must not contain newlines")
		}
	}
	return nil
}

func hasCRLF(s string) bool {
	return strings.ContainsAny(s, "\r\n")
}
