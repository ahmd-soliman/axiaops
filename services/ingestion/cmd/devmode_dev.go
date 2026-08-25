//go:build !production

package main

import "os"

// devModeEnabled reports whether the DEV_MODE bypass is active. See
// services/api/cmd/devmode_dev.go for the full rationale — the ingestion
// service mirrors api's build-tag split for B1.7 layer 3 (plan §4.10.2)
// so the bypass story stays symmetric: DEV_MODE also relaxes ingestion's
// HMAC verification to a passthrough (no INGESTION_SHARED_SECRET required)
// and permits the no-account synthetic scan path. Stripping it from one
// binary but not the other would leave a half-defended posture.
func devModeEnabled() bool {
	return os.Getenv("DEV_MODE") == "true"
}
