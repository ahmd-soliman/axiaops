package notifications

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"axiaops.io/shared/model"
)

// InviteSender is the transport primitive the API's invite mailer uses to
// deliver an invitation email. Keeping it a separate interface (rather than
// widening Transport) means only the email transport grows the method —
// Slack/Teams/Jira transports stay digest-only.
//
// It takes a plaintext EmailConfig rather than a NotificationChannel so the
// caller owns config resolution: the api mailer sources the config from either
// the org's email notification channel (via DecodeEmailConfig) or the global
// env/SSM SMTP settings, then hands the resolved config here.
type InviteSender interface {
	// SendInvite delivers a team-invitation email to a single recipient using
	// the supplied SMTP config. cfg.Recipients (if any) is ignored — an invite
	// targets exactly the invitee. Best-effort: the caller logs the error and
	// still returns the OOB redemption URL.
	SendInvite(ctx context.Context, cfg model.EmailConfig, recipient string, inv InviteEmail) error
}

// InviteEmail is the transport-agnostic content for one invitation message.
// The redemption URL must be absolute — a relative link is useless in an email,
// so the API skips sending when PUBLIC_HOST is unset rather than mailing a dead
// link.
type InviteEmail struct {
	OrganizationName string    // org display name; falls back to a generic phrase when empty
	Role             string    // role the invitee is being granted (admin/member/viewer)
	InviterEmail     string    // who sent the invite — shown so the invitee can vouch it
	RedemptionURL    string    // absolute accept-invite URL carrying the token
	ExpiresAt        time.Time // when the invitation token stops being redeemable
}

// SendInvite implements InviteSender on the SMTP email transport. It reuses the
// same validate → timeout-bounded deliver path as the scan digest, swapping
// only the message body and the recipient. cfg is already-resolved plaintext
// (the caller decrypted a channel or read the global SMTP env config).
func (t *EmailTransport) SendInvite(ctx context.Context, cfg model.EmailConfig, recipient string, inv InviteEmail) error {
	if err := ValidateEmailConfig(cfg); err != nil {
		return err
	}
	recipient = strings.TrimSpace(recipient)
	if recipient == "" {
		return fmt.Errorf("email: invite recipient is required")
	}
	return t.deliver(ctx, cfg, []string{recipient}, buildInviteMessage(cfg, recipient, inv))
}

// buildInviteMessage composes the RFC 5322 invite message (headers + plaintext
// body). Plaintext only, mirroring the digest — HTML is deferred for the same
// Outlook-CSS/MIME reasons.
func buildInviteMessage(cfg model.EmailConfig, recipient string, inv InviteEmail) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", formatFromHeader(cfg))
	fmt.Fprintf(&b, "To: %s\r\n", (&mail.Address{Address: recipient}).String())
	// headerValue (audit N-2): the subject interpolates the org display name —
	// admin-controlled, and under self-serve signup attacker-controlled. The
	// creation paths reject newlines, but the Subject line must be safe even if
	// a future org-name write path forgets that check.
	fmt.Fprintf(&b, "Subject: %s\r\n", headerValue(renderInviteSubject(inv)))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(renderInviteBody(inv))
	return []byte(b.String())
}

// inviteOrgFallback names the org generically when the org row has a blank
// display name, so the subject/body never reads "invited you to ." .
const inviteOrgFallback = "an organization"

func renderInviteSubject(inv InviteEmail) string {
	org := inv.OrganizationName
	if org == "" {
		org = inviteOrgFallback
	}
	return fmt.Sprintf("You've been invited to %s on AxiaOps", org)
}

func renderInviteBody(inv InviteEmail) string {
	org := inv.OrganizationName
	if org == "" {
		org = inviteOrgFallback
	}
	var b strings.Builder
	if inv.InviterEmail != "" {
		fmt.Fprintf(&b, "%s has invited you to join %s on AxiaOps", inv.InviterEmail, org)
	} else {
		fmt.Fprintf(&b, "You've been invited to join %s on AxiaOps", org)
	}
	if inv.Role != "" {
		fmt.Fprintf(&b, " as a %s", inv.Role)
	}
	b.WriteString(".\r\n")
	b.WriteString("\r\nAxiaOps finds idle and zombie cloud resources that still cost money.\r\n")
	fmt.Fprintf(&b, "\r\nAccept your invitation:\r\n%s\r\n", inv.RedemptionURL)
	if !inv.ExpiresAt.IsZero() {
		fmt.Fprintf(&b, "\r\nThis invitation expires on %s.\r\n", inv.ExpiresAt.UTC().Format("2 January 2006"))
	}
	b.WriteString("\r\nIf you weren't expecting this invitation, you can ignore this email.\r\n")
	return b.String()
}
