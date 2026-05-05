//go:build production

package main

// devModeEnabled is hard-wired to false in production-tagged ingestion
// builds. Mirrors services/api/cmd/devmode_production.go — see that file
// for the full layer-3 rationale.
func devModeEnabled() bool {
	return false
}
