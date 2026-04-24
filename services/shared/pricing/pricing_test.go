package pricing_test

import (
	"reflect"
	"testing"

	"axiaops.io/shared/pricing"
)

func TestDefault_ParsesAndAllRatesNonZero(t *testing.T) {
	cfg := pricing.Default()

	if cfg.Currency != "USD" {
		t.Errorf("currency = %q; want USD", cfg.Currency)
	}

	r := cfg.Default
	v := reflect.ValueOf(r)
	typ := v.Type()
	for i := 0; i < v.NumField(); i++ {
		val := v.Field(i).Float()
		if val == 0 {
			t.Errorf("default rate %s is zero — every rate must be populated in rates.yml", typ.Field(i).Name)
		}
	}
}

func TestFor_UnknownRegion_ReturnsDefault(t *testing.T) {
	cfg := pricing.Default()
	got := cfg.For("mars-north-1")
	if got != cfg.Default {
		t.Errorf("unknown region should fall back to default; got %+v", got)
	}
}

func TestFor_RegionOverride_MergesFieldByField(t *testing.T) {
	yaml := []byte(`
currency: USD
default:
  eip_monthly: 3.60
  ebs_volume_gb_monthly: 0.08
  ebs_snapshot_gb_monthly: 0.05
  cw_logs_gb_monthly: 0.03
  rds_snapshot_gb_monthly: 0.095
  ecr_storage_gb_monthly: 0.10
  kinesis_shard_hourly: 0.015
  secret_monthly: 0.40
regions:
  eu-central-1:
    eip_monthly: 4.00
    ebs_volume_gb_monthly: 0.088
`)
	cfg, err := pricing.Parse(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r := cfg.For("eu-central-1")

	if r.EIPMonthly != 4.00 {
		t.Errorf("EIPMonthly = %v; want override 4.00", r.EIPMonthly)
	}
	if r.EBSVolumeGBMonthly != 0.088 {
		t.Errorf("EBSVolumeGBMonthly = %v; want override 0.088", r.EBSVolumeGBMonthly)
	}
	// Fields not in the override must fall back to default.
	if r.SecretMonthly != 0.40 {
		t.Errorf("SecretMonthly = %v; want default 0.40 (no override)", r.SecretMonthly)
	}
	if r.KinesisShardHourly != 0.015 {
		t.Errorf("KinesisShardHourly = %v; want default 0.015 (no override)", r.KinesisShardHourly)
	}
}

func TestDefault_EUCentral1_VerifiedRates(t *testing.T) {
	// Values verified 2026-04-24 against the AWS Price List Bulk API.
	// If this test fails after a rates.yml edit, re-verify against AWS before
	// updating the expected value — don't just match the YAML blindly.
	cfg := pricing.Default()
	eu := cfg.For("eu-central-1")

	cases := []struct {
		name string
		got  float64
		want float64
	}{
		{"EBSVolumeGBMonthly", eu.EBSVolumeGBMonthly, 0.0952},
		{"EBSSnapshotGBMonthly", eu.EBSSnapshotGBMonthly, 0.054},
		{"CWLogsGBMonthly", eu.CWLogsGBMonthly, 0.0324},
		{"RDSSnapshotGBMonthly", eu.RDSSnapshotGBMonthly, 0.103},
		{"KinesisShardHourly", eu.KinesisShardHourly, 0.018},
		// Fields verified uniform with us-east-1 on 2026-04-24.
		{"EIPMonthly", eu.EIPMonthly, 3.60},
		{"ECRStorageGBMonthly", eu.ECRStorageGBMonthly, 0.10},
		{"SecretMonthly", eu.SecretMonthly, 0.40},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("eu-central-1 %s = %v; want %v", c.name, c.got, c.want)
		}
	}
}

func TestParse_InvalidYAML_ReturnsError(t *testing.T) {
	_, err := pricing.Parse([]byte("not: valid: yaml: ["))
	if err == nil {
		t.Error("expected parse error on malformed YAML")
	}
}
