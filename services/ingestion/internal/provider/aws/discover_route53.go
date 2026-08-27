package aws

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"

	"axiaops.io/shared/model"
)

const Route53ZoneMonthlyCost = 0.50

// DiscoverUnusedHostedZones lists all Route53 hosted zones. A zone is flagged
// as a zombie if it contains only standard default NS and SOA records (ResourceRecordSetCount <= 2).
func DiscoverUnusedHostedZones(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	cfg, err := awsClient.configForRegion(ctx, "us-east-1")
	if err != nil {
		return nil, fmt.Errorf("route53: load config: %w", err)
	}

	client := route53.NewFromConfig(cfg)
	var zombies []model.ZombieResource
	accountID := awsClient.AccountID()

	var marker *string
	for {
		out, err := client.ListHostedZones(ctx, &route53.ListHostedZonesInput{
			Marker: marker,
		})
		if err != nil {
			slog.Warn("route53: ListHostedZones", "error", err)
			return nil, nil
		}

		for _, zone := range out.HostedZones {
			// If a hosted zone has 2 or fewer record sets, it contains only SOA and NS records
			if zone.ResourceRecordSetCount != nil && aws.ToInt64(zone.ResourceRecordSetCount) <= 2 {
				zoneID := aws.ToString(zone.Id)
				zoneName := aws.ToString(zone.Name)
				z := model.ZombieResource{
					InternalAccountID: internalAccountID,
					AccountID:         accountID,
					Provider:          "aws",
					Service:           "AmazonRoute53",
					ResourceType:      "route53_zone",
					ResourceID:        zoneID,
					Region:            "global",
					Tags:              map[string]string{"Name": zoneName},
					MonthlyCost:       Route53ZoneMonthlyCost,
					Currency:          awsClient.Currency(),
					PeriodStart:       start,
					PeriodEnd:         end,
					UsageMetric:       "QueryCount",
					UsageAvg:          0,
					UsageUnit:         "Count",
					Reason:            "Route53 hosted zone has no custom DNS records — likely abandoned",
					Owner:             "unknown",
				}
				zombies = append(zombies, z)
				slog.Info("route53: unused hosted zone flagged", "zone_id", zoneID, "name", zoneName)
			}
		}

		if !out.IsTruncated || out.NextMarker == nil {
			break
		}
		marker = out.NextMarker
	}

	return zombies, nil
}
