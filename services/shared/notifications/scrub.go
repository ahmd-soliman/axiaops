package notifications

import "strings"

// scrubSecrets replaces every non-empty secret with "***" in s. Used by
// transports to keep bearer tokens (Slack webhook URLs, SMTP passwords) out of
// the error strings that land in notification_dispatches.error and the
// dashboard's deliveries drawer — e.g. Slack's 404 body echoes the webhook URL.
func scrubSecrets(s string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "***")
	}
	return s
}
