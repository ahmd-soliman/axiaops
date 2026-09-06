package aws

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"axiaops.io/shared/model"
)

// unusedSecretThreshold is the minimum time since a secret was last accessed
// before it is flagged as unused. 90 days is conservative — secrets accessed
// quarterly (e.g. rotation) are excluded.
const unusedSecretThreshold = 90 * 24 * time.Hour

// isSecretUnused returns the number of days since last access if the secret
// should be flagged, or -1 if the secret is still in use.
func isSecretUnused(lastAccessed, created *time.Time, threshold time.Duration, now time.Time) int {
	if lastAccessed != nil {
		age := now.Sub(*lastAccessed)
		if age < threshold {
			return -1
		}
		return int(age.Hours() / 24)
	}
	if created != nil {
		age := now.Sub(*created)
		if age < threshold {
			return -1
		}
		return int(age.Hours() / 24)
	}
	return -1
}

// DiscoverUnusedSecrets calls secretsmanager:ListSecrets in each region present
// in the cost records and returns a ZombieResource for every secret whose
// LastAccessedDate is older than 90 days (or was never accessed). Secrets are
// billed at $0.40/month regardless of whether they are read, so forgotten
// secrets accumulate charges silently after the service that used them is torn down.
func DiscoverUnusedSecrets(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := discoveryRegions(records, awsClient.Region())
	accountID := awsClient.AccountID()
	now := time.Now().UTC()
	var zombies []model.ZombieResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("secrets: load config", "region", region, "error", err)
			continue
		}
		client := secretsmanager.NewFromConfig(cfg)
		rates := awsClient.Rates(region)

		var nextToken *string
		for {
			out, err := client.ListSecrets(ctx, &secretsmanager.ListSecretsInput{
				NextToken: nextToken,
			})
			if err != nil {
				slog.Warn("secrets: ListSecrets failed", "region", region, "error", err)
				break
			}

			for _, s := range out.SecretList {
				name := aws.ToString(s.Name)
				if name == "" {
					continue
				}

				daysSinceAccess := isSecretUnused(s.LastAccessedDate, s.CreatedDate, unusedSecretThreshold, now)
				if daysSinceAccess < 0 {
					continue
				}

				zombies = append(zombies, model.ZombieResource{
					Provider:          "aws",
					AccountID:         accountID,
					InternalAccountID: internalAccountID,
					Service:           "AWSSecretsManager",
					Region:            region,
					ResourceID:        name,
					Tags:              map[string]string{},
					MonthlyCost:       rates.SecretMonthly,
					Currency:          awsClient.Currency(),
					PeriodStart:       start,
					PeriodEnd:         end,
					UsageMetric:       "DaysSinceAccess",
					UsageAvg:          float64(daysSinceAccess),
					UsageUnit:         "Days",
					Reason:            fmt.Sprintf("Secret not accessed for %d days — still billing %.2f %s/month", daysSinceAccess, rates.SecretMonthly, awsClient.Currency()),
					Owner:             "unknown",
				})
				slog.Info("secrets: unused secret flagged", "name", name, "days_since_access", daysSinceAccess, "region", region)
			}

			if out.NextToken == nil {
				break
			}
			nextToken = out.NextToken
		}
	}
	return zombies, nil
}
