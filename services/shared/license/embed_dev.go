//go:build !production

package license

import _ "embed"

// devEmbeddedPubKeyPEM is the dev-only RS256 public key. Verifies the
// embedded dev fixture JWT (devFixtureJWT below) so DEV_MODE boots through
// the same Load() + CheckExpiry chain as a real customer rather than
// short-circuiting via the legacy enforcementBypass flag — closes the
// dev/prod-parity gap that B1.7 layer 4 (issue #75) was filed for.
//
// **Threat model.** This key is published in source control. A leaked dev
// fixture is useless against a customer-shipping binary because the
// production-tagged sibling (embed_production.go) defines this var as nil
// — `Load()` never offers the dev key for verification in that build, so
// the fixture's signature is rejected against the production pubkey.
// Layer 3 (`-tags production`) is the structural defense; this file is the
// dev-side mirror.
//
// Re-mint procedure (rare — only when the dev fixture's claim shape needs
// to change; the JWT itself is good for 100 years). Run from the repo root
// so the relative paths below resolve correctly:
//
//  1. `openssl genrsa -out /tmp/dev-private.pem 4096`
//  2. `openssl rsa -in /tmp/dev-private.pem -pubout -out services/shared/license/pubkey-dev.pem`
//  3. `LICENSE_SIGNING_KEY_PATH=/tmp/dev-private.pem go run ./services/api/cmd/license-issue \
//        -customer-id=axiaops-dev-fixture -contract-id=DEV-FIXTURE-100Y -days=36500 \
//        -max-organizations=10 -features=base -grace-period-days=0 \
//        > services/shared/license/fixture-dev.jwt`
//  4. `rm -P /tmp/dev-private.pem` (destroy — never commit, never archive)
//
//go:embed pubkey-dev.pem
var devEmbeddedPubKeyPEM []byte

// devFixtureJWT is the 100-year dev license JWT signed by the dev key
// above. VerifyAtBoot loads this in dev builds when the operator has
// neither set AXIAOPS_LICENSE nor placed a license file at the resolved
// path — the analogue of the auto-generated bootstrap install token,
// scoped to the license posture.
//
// `customer_id=axiaops-dev-fixture` is the operator-recognisable signal
// that this binary is running on the dev fixture rather than a real
// customer license; `/v1/version` and the LicenseBanner / Settings →
// License pane both surface it verbatim.
//
//go:embed fixture-dev.jwt
var devFixtureJWT []byte
