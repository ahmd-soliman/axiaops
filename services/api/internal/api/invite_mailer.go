package api

import (
	"context"
	"log/slog"
	"time"

	"axiaops.io/shared/model"
	"axiaops.io/shared/notifications"
	"axiaops.io/shared/observability"
)

// InviteMailer delivers an invitation's redemption URL to the invitee. It is the
// composition-time seam (wired onto the Handler via WithInviteMailer) that a
// future impl could swap for a platform mailer (Resend/Postmark) without
// touching the handler. SendInvite is best-effort and self-contained: every
// failure mode resolves to an inviteEmail* outcome string rather than an error,
// because the redemption URL is already in the invitation response — emailing it
// is a convenience, not a correctness requirement.
type InviteMailer interface {
	SendInvite(ctx context.Context, req InviteMailRequest) (outcome string)
}

// InviteMailRequest is everything the mailer needs to compose + address one
// invitation email. RedemptionURL is the absolute/relative URL already returned
// to the admin; the default mailer skips sending when it is not absolute.
type InviteMailRequest struct {
	OrganizationID string
	Recipient      string
	Role           string
	InviterEmail   string
	RedemptionURL  string
	ExpiresAt      time.Time
}

// inviteMailerStore is the narrow slice of storage.Store the default mailer
// reads — the org's enabled channels (to source per-org SMTP) and the org name
// (for the message). Narrow on purpose, so the mailer is trivially faked.
//
// Both reads are org-scoped: the store MUST be the RLS-enforced request pool
// (not the admin pool), and the ctx passed to SendInvite MUST already carry the
// caller's organization_id (createInvitation sets it via WithOrganizationID
// before calling the seam). A cross-org pool here would leak another org's
// channel config into the invite.
type inviteMailerStore interface {
	ListEnabledNotificationChannels(ctx context.Context) ([]model.NotificationChannel, error)
	GetOrganizationByID(ctx context.Context, id string) (model.Organization, error)
}

// defaultInviteMailer resolves the SMTP config from the org's first enabled
// email notification channel, falling back to the global env/SSM SMTP config
// (SMTP_HOST/…, the platform transactional mailer — e.g. a Gmail SMTP relay).
//
// Precedence is channel-first, global-fallback: an org that configured its own
// email channel sends invites from its own relay; everyone else (including an
// org that disabled its digest channel) falls through to the system mailer.
// This is the seam the architecture review asked for — invite delivery no
// longer hard-couples to the per-org digest channel.
type defaultInviteMailer struct {
	store        inviteMailerStore
	transport    notifications.InviteSender
	globalConfig model.EmailConfig // zero value (empty SMTPHost) ⇒ no global mailer configured
	publicHost   string
	timeout      time.Duration
}

// NewInviteMailer builds the default channel-first / global-fallback mailer.
// globalConfig is the env/SSM SMTP config (zero ⇒ unset). transport is the
// shared email transport (the same *EmailTransport wired for channel /test).
func NewInviteMailer(store inviteMailerStore, transport notifications.InviteSender, globalConfig model.EmailConfig, publicHost string) *defaultInviteMailer {
	return &defaultInviteMailer{
		store:        store,
		transport:    transport,
		globalConfig: globalConfig,
		publicHost:   publicHost,
		timeout:      notifications.DefaultSendTimeout,
	}
}

func (m *defaultInviteMailer) SendInvite(ctx context.Context, req InviteMailRequest) string {
	if m.publicHost == "" {
		// req.RedemptionURL is relative — useless in an email. Admin shares OOB.
		return recordInviteEmail(inviteEmailSkippedNoHost, inviteSourceNone)
	}

	cfg, source, skip := m.resolveConfig(ctx)
	if skip != "" {
		return recordInviteEmail(skip, source)
	}

	// Org display name for the subject/body. Non-fatal — the invite builder
	// falls back to a generic phrase on an empty name.
	var orgName string
	if org, err := m.store.GetOrganizationByID(ctx, req.OrganizationID); err != nil {
		slog.Debug("invite mailer: org name lookup failed", "error", err, "organization_id", req.OrganizationID)
	} else {
		orgName = org.Name
	}

	// Detach the SMTP send from the request context: a client disconnect must
	// not abort an in-flight relay handshake and mislabel a delivered message as
	// failed. Bounded by its own timeout + dialingSendMail's per-conn deadline.
	sendCtx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	err := m.transport.SendInvite(sendCtx, cfg, req.Recipient, notifications.InviteEmail{
		OrganizationName: orgName,
		Role:             req.Role,
		InviterEmail:     req.InviterEmail,
		RedemptionURL:    req.RedemptionURL,
		ExpiresAt:        req.ExpiresAt,
	})
	if err != nil {
		// transport.SendInvite has already scrubbed any SMTP secret from err.
		slog.Warn("invite mailer: delivery failed",
			"error", err, "organization_id", req.OrganizationID, "source", source)
		return recordInviteEmail(inviteEmailFailed, source)
	}
	slog.Info("invite mailer: invite email sent", "organization_id", req.OrganizationID, "source", source)
	return recordInviteEmail(inviteEmailSent, source)
}

// resolveConfig picks the SMTP config for this send.
//
//   - (cfg, "channel", "")  — the org's first enabled email channel, decrypted.
//   - (cfg, "global", "")   — the global env/SSM SMTP config.
//   - (zero, "none", inviteEmailSkippedNoTransport) — neither configured.
//   - (zero, "none", inviteEmailError)              — the channel-list read failed.
func (m *defaultInviteMailer) resolveConfig(ctx context.Context) (model.EmailConfig, string, string) {
	channels, err := m.store.ListEnabledNotificationChannels(ctx)
	if err != nil {
		slog.Warn("invite mailer: list channels failed", "error", err)
		return model.EmailConfig{}, inviteSourceNone, inviteEmailError
	}
	for _, ch := range channels {
		if ch.Kind != model.ChannelKindEmail {
			continue
		}
		cfg, derr := notifications.DecodeEmailConfig(ch)
		if derr != nil {
			// A misconfigured channel shouldn't strand the invite — log and try
			// the next email channel; the global mailer is the last resort once
			// every email channel is exhausted.
			slog.Warn("invite mailer: channel config unusable, trying next", "error", derr, "channel_id", ch.ID)
			continue
		}
		return cfg, inviteSourceChannel, ""
	}
	if m.globalConfig.SMTPHost != "" {
		if verr := notifications.ValidateEmailConfig(m.globalConfig); verr != nil {
			slog.Warn("invite mailer: global SMTP config invalid", "error", verr)
			return model.EmailConfig{}, inviteSourceNone, inviteEmailError
		}
		return m.globalConfig, inviteSourceGlobal, ""
	}
	return model.EmailConfig{}, inviteSourceNone, inviteEmailSkippedNoTransport
}

// invite-email metric source labels.
const (
	inviteSourceChannel = "channel"
	inviteSourceGlobal  = "global"
	inviteSourceNone    = "none"
)

// recordInviteEmail meters the outcome and returns it unchanged, so call sites
// stay one-liners (return recordInviteEmail(outcome, source)).
func recordInviteEmail(outcome, source string) string {
	observability.Global.AuthInviteEmailTotal.WithLabelValues(outcome, source).Inc()
	return outcome
}
