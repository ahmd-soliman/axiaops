// Package analyzer detects zombie cloud resources by joining cost records with
// usage metrics and applying per-service threshold rules.
package analyzer

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadUsageFixture reads usage records from a JSON file on disk.
// Used in dev mode; production will source these from CloudWatch.
func LoadUsageFixture(path string) ([]UsageRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("usage fixture: open %s: %w", path, err)
	}
	defer f.Close()

	var records []UsageRecord
	if err := json.NewDecoder(f).Decode(&records); err != nil {
		return nil, fmt.Errorf("usage fixture: decode: %w", err)
	}
	return records, nil
}
