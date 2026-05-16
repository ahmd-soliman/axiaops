//go:build production

package license

// devEmbeddedPubKeyPEM is nil in production builds. The dev-only RS256
// public key is NOT compiled in here — `-tags production` strips
// embed_dev.go from the build entirely, leaving Load()'s dev-key fallback
// branch unreachable because the slice it would offer is empty.
//
// This is the dev-fixture half of B1.7 layer 3 (plan §4.10.2): dev binaries
// trust both keys, customer-shipping binaries trust only the production
// pubkey. A leaked dev fixture cannot bypass enforcement in a customer's
// install because the customer's binary has no dev pubkey to verify it
// against. Pair this file with cmd/devmode_production.go — both seams must
// be tag-stripped together for the layer to hold.
var devEmbeddedPubKeyPEM []byte

// devFixtureJWT is nil in production builds. The 100-year dev fixture is
// not compiled into customer binaries — paired with the empty
// devEmbeddedPubKeyPEM above, VerifyAtBoot's dev-fixture auto-load branch
// cannot fire even if a malicious actor managed to coerce devModeEnabled()
// into returning true (which itself is structurally impossible — the
// production-tagged devmode_production.go hard-wires it to false).
var devFixtureJWT []byte
