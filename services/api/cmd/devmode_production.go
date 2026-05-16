//go:build production

package main

// devModeEnabled is hard-wired to false in production builds — DEV_MODE is
// read but ignored, satisfying B1.7 layer 3 (plan §4.10.2). Customer-
// shipping binaries are built with `-tags production` via the Makefile's
// `build-production` target; the CI image axiaops-api:{semver} consumes
// the production-tagged build, while internal axiaops-api:dev-{commit}
// uses the default (no tag).
//
// The function exists in both build modes — same name, same signature —
// so cmd/main.go has no #ifdef-style branches. The compiler dead-codes
// every `if devModeEnabled() { ... }` branch at production-build time.
//
// Pair with services/dashboard/Dockerfile's `ARG VITE_DEV_MODE=false`
// default: production-built dashboard images bake DEV_MODE=false at the
// vite build step, so the frontend cannot short-circuit auth either.
func devModeEnabled() bool {
	return false
}
