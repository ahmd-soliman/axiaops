//go:build !production

package main

import "os"

// devModeEnabled reports whether the DEV_MODE bypass is active in this
// process. Default-build implementation reads the runtime env var so local
// `make start-dev` and the on-prem dev-1 / dev-2 slots can opt in.
//
// The production-tagged sibling (devmode_production.go) hard-wires this to
// false — that is B1.7 layer 3 (plan §4.10.2): customer-shipping binaries
// built with `-tags production` ignore DEV_MODE entirely, closing the
// "operator with shell access flips DEV_MODE to bypass auth" attack path —
// DEV_MODE replaces the entire auth chain with DevBypass, so this control
// stands on its own.
//
// Every cmd/main.go site that previously read os.Getenv("DEV_MODE")=="true"
// routes through this helper — the build-tag split lives at one seam.
func devModeEnabled() bool {
	return os.Getenv("DEV_MODE") == "true"
}
