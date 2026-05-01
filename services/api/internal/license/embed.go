package license

import _ "embed"

// embeddedPubKeyPEM is the RS256 public key compiled into the binary.
//
// Plan §4.9: the production key is generated offline, the matching private
// key custody-chained into the operator's secret store (Bitwarden / 1Password
// / Vault / HSM — the package does not care), and only this public half is
// committed. The runbook for issuing the production key lives in
// docs/license-issuance.md (slice 7).
//
// The current value is a placeholder generated during slice 1 with its
// matching private key destroyed at generation time. Before the first paying
// customer, slice-7 ceremony swaps it for the production key.
//
//go:embed pubkey.pem
var embeddedPubKeyPEM []byte
