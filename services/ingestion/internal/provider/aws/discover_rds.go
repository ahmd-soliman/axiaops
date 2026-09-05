package aws

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"

	"axiaops.io/shared/model"
)

func discoverRDS(ctx context.Context, cfg aws.Config) []string {
	client := rds.NewFromConfig(cfg)
	var ids []string
	var marker *string
	for {
		out, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
			Marker: marker,
		})
		if err != nil {
			slog.Warn("discover: RDS DescribeDBInstances", "error", err)
			return nil
		}
		for _, db := range out.DBInstances {
			if db.DBInstanceIdentifier != nil {
				ids = append(ids, aws.ToString(db.DBInstanceIdentifier))
			}
		}
		if out.Marker == nil {
			break
		}
		marker = out.Marker
	}
	return ids
}

// rdsSnapshotAgeThreshold is the minimum age for a manual RDS snapshot to be
// considered stale. Snapshots younger than this are left alone — they may be
// part of a recent migration or manual backup workflow.
const rdsSnapshotAgeThreshold = 30 * 24 * time.Hour

// DiscoverOrphanedRDSSnapshots calls rds:DescribeDBSnapshots (type=manual)
// cross-referenced with rds:DescribeDBInstances to find manual snapshots whose
// source DB instance no longer exists and that are older than 30 days. These
// accumulate silently at $0.095/GB-month. Automated snapshots are excluded —
// AWS manages their lifecycle via the retention setting.
func DiscoverOrphanedRDSSnapshots(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := discoveryRegions(records, awsClient.Region())
	accountID := awsClient.AccountID()
	now := time.Now().UTC()
	var zombies []model.ZombieResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("rds-snap: load config", "region", region, "error", err)
			continue
		}
		client := rds.NewFromConfig(cfg)

		// 1. Build the set of DB instance identifiers that currently exist.
		//    If this call fails we cannot safely distinguish "orphaned" from
		//    "source DB exists but DescribeDBInstances returned an error", so
		//    skip the entire region rather than producing false positives.
		existingDBs := make(map[string]struct{})
		var dbFetchFailed bool
		var dbMarker *string
		for {
			dbOut, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
				Marker: dbMarker,
			})
			if err != nil {
				slog.Warn("rds-snap: DescribeDBInstances failed", "region", region, "error", err)
				dbFetchFailed = true
				break
			}
			for _, db := range dbOut.DBInstances {
				if db.DBInstanceIdentifier != nil {
					existingDBs[aws.ToString(db.DBInstanceIdentifier)] = struct{}{}
				}
			}
			if dbOut.Marker == nil {
				break
			}
			dbMarker = dbOut.Marker
		}
		if dbFetchFailed {
			continue
		}

		// 2. Enumerate manual snapshots and flag orphans.
		rates := awsClient.Rates(region)
		var snapMarker *string
		for {
			snapOut, err := client.DescribeDBSnapshots(ctx, &rds.DescribeDBSnapshotsInput{
				SnapshotType: aws.String("manual"),
				Marker:       snapMarker,
			})
			if err != nil {
				slog.Warn("rds-snap: DescribeDBSnapshots failed", "region", region, "error", err)
				break
			}

			for _, snap := range snapOut.DBSnapshots {
				snapID := aws.ToString(snap.DBSnapshotIdentifier)
				dbID := aws.ToString(snap.DBInstanceIdentifier)

				_, sourceExists := existingDBs[dbID]
				if snap.SnapshotCreateTime == nil {
					continue
				}
				ageDays := isRDSSnapshotOrphaned(sourceExists, now.Sub(*snap.SnapshotCreateTime), rdsSnapshotAgeThreshold)
				if ageDays < 0 {
					continue
				}

				sizeGB := aws.ToInt32(snap.AllocatedStorage)
				monthlyCost := float64(sizeGB) * rates.RDSSnapshotGBMonthly

				zombies = append(zombies, model.ZombieResource{
					Provider:          "aws",
					AccountID:         accountID,
					InternalAccountID: internalAccountID,
					Service:           "AmazonRDS",
					Region:            region,
					ResourceID:        snapID,
					Tags:              map[string]string{},
					MonthlyCost:       monthlyCost,
					Currency:          awsClient.Currency(),
					PeriodStart:       start,
					PeriodEnd:         end,
					UsageMetric:       "SourceDBExists",
					UsageAvg:          float64(ageDays),
					UsageUnit:         "Days",
					Reason:            fmt.Sprintf("Manual RDS snapshot (%d GB, %d days old) is orphaned — source DB %q no longer exists, accumulating $%.2f/month in storage charges", sizeGB, ageDays, dbID, monthlyCost),
					Owner:             "unknown",
				})
				slog.Info("rds-snap: orphaned snapshot flagged", "snapshot_id", snapID, "source_db", dbID, "size_gb", sizeGB, "age_days", ageDays, "region", region)
			}

			if snapOut.Marker == nil {
				break
			}
			snapMarker = snapOut.Marker
		}
	}
	return zombies, nil
}

// isRDSSnapshotOrphaned returns the age in days if the snapshot should be
// flagged, or -1 if it's not a zombie. A snapshot is orphaned when its source
// DB no longer exists and it's older than the threshold.
func isRDSSnapshotOrphaned(sourceDBExists bool, snapshotAge time.Duration, threshold time.Duration) int {
	if sourceDBExists {
		return -1
	}
	if snapshotAge < threshold {
		return -1
	}
	return int(snapshotAge.Hours() / 24)
}
