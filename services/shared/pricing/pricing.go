// Package pricing provides AWS list-price rates used to estimate the monthly
// cost of idle/zombie resources. Rates live in rates.yml (embedded at build
// time) rather than as Go constants so they can be reviewed, updated, and
// region-overridden as a single config diff.
package pricing

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed rates.yml
var defaultYAML []byte

// Rates holds the per-resource list prices used by discover.go to compute
// MonthlyCost on zombie resources. A zero value means "no override" —
// Config.For() merges the default set with region-specific overrides.
type Rates struct {
	EIPMonthly           float64 `yaml:"eip_monthly"`
	EBSVolumeGBMonthly   float64 `yaml:"ebs_volume_gb_monthly"`
	EBSSnapshotGBMonthly float64 `yaml:"ebs_snapshot_gb_monthly"`
	CWLogsGBMonthly      float64 `yaml:"cw_logs_gb_monthly"`
	RDSSnapshotGBMonthly float64 `yaml:"rds_snapshot_gb_monthly"`
	ECRStorageGBMonthly  float64 `yaml:"ecr_storage_gb_monthly"`
	KinesisShardHourly   float64 `yaml:"kinesis_shard_hourly"`
	SecretMonthly        float64 `yaml:"secret_monthly"`
}

// Config is the parsed rates.yml document: a canonical `default` rate set
// plus optional per-region overrides.
type Config struct {
	Currency string           `yaml:"currency"`
	Default  Rates            `yaml:"default"`
	Regions  map[string]Rates `yaml:"regions"`
}

// Default loads the embedded rates.yml. Panics on parse error — the YAML is
// checked into the repo and its integrity is a build-time concern, so a
// broken file should be caught in CI, not at runtime.
func Default() *Config {
	cfg, err := Parse(defaultYAML)
	if err != nil {
		panic(fmt.Sprintf("pricing: embedded rates.yml is invalid: %v", err))
	}
	return cfg
}

// Parse decodes a YAML document into a Config. Exposed for tests.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("pricing: unmarshal: %w", err)
	}
	if cfg.Currency == "" {
		cfg.Currency = "USD"
	}
	return &cfg, nil
}

// For returns the effective Rates for the given AWS region: default values
// overlaid with any fields set in regions[region]. Unknown regions get the
// default set unchanged.
func (c *Config) For(region string) Rates {
	r := c.Default
	override, ok := c.Regions[region]
	if !ok {
		return r
	}
	if override.EIPMonthly != 0 {
		r.EIPMonthly = override.EIPMonthly
	}
	if override.EBSVolumeGBMonthly != 0 {
		r.EBSVolumeGBMonthly = override.EBSVolumeGBMonthly
	}
	if override.EBSSnapshotGBMonthly != 0 {
		r.EBSSnapshotGBMonthly = override.EBSSnapshotGBMonthly
	}
	if override.CWLogsGBMonthly != 0 {
		r.CWLogsGBMonthly = override.CWLogsGBMonthly
	}
	if override.RDSSnapshotGBMonthly != 0 {
		r.RDSSnapshotGBMonthly = override.RDSSnapshotGBMonthly
	}
	if override.ECRStorageGBMonthly != 0 {
		r.ECRStorageGBMonthly = override.ECRStorageGBMonthly
	}
	if override.KinesisShardHourly != 0 {
		r.KinesisShardHourly = override.KinesisShardHourly
	}
	if override.SecretMonthly != 0 {
		r.SecretMonthly = override.SecretMonthly
	}
	return r
}
