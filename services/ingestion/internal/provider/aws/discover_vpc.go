package aws

import (
	"context"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"axiaops.io/shared/model"
)

func discoverNATGateways(ctx context.Context, cfg aws.Config) []string {
	client := ec2.NewFromConfig(cfg)
	out, err := client.DescribeNatGateways(ctx, &ec2.DescribeNatGatewaysInput{})
	if err != nil {
		slog.Warn("discover: EC2 DescribeNatGateways", "error", err)
		return nil
	}
	var ids []string
	for _, ng := range out.NatGateways {
		if ng.NatGatewayId != nil {
			ids = append(ids, aws.ToString(ng.NatGatewayId))
		}
	}
	return ids
}

// DiscoverUnattachedEIPs calls ec2:DescribeAddresses in each region present in
// the cost records and returns a ZombieResource for every Elastic IP that is not
// attached to a network interface. Unattached EIPs are always zombies — AWS
// charges for them regardless of usage, with no CloudWatch metric to consult.
// internalAccountID is the UUID from the accounts table, used for filtering.
func DiscoverUnattachedEIPs(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := discoveryRegions(records, awsClient.Region())
	accountID := awsClient.AccountID()
	var zombies []model.ZombieResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("eip: load config for region", "region", region, "error", err)
			continue
		}

		client := ec2.NewFromConfig(cfg)
		out, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{})
		if err != nil {
			slog.Warn("eip: DescribeAddresses", "region", region, "error", err)
			continue
		}

		rates := awsClient.Rates(region)
		for _, addr := range out.Addresses {
			// An EIP is a zombie when it has no attached network interface.
			if addr.NetworkInterfaceId != nil {
				continue
			}
			allocationID := aws.ToString(addr.AllocationId)
			if allocationID == "" {
				continue
			}

			tags := ec2TagsToMap(addr.Tags)
			zombies = append(zombies, model.ZombieResource{
				Provider:          "aws",
				AccountID:         accountID,
				InternalAccountID: internalAccountID,
				Service:           "AmazonVPC",
				Region:            region,
				ResourceID:        allocationID,
				Tags:              tags,
				MonthlyCost:       rates.EIPMonthly,
				Currency:          awsClient.Currency(),
				PeriodStart:       start,
				PeriodEnd:         end,
				UsageMetric:       "NetworkInterfaceAttachment",
				UsageAvg:          0,
				UsageUnit:         "Count",
				Reason:            "Elastic IP not attached to any resource — incurring $0.005/hour idle charge",
				Owner:             ownerFromTags(tags),
			})
			slog.Info("eip: unattached EIP flagged", "allocation_id", allocationID, "region", region)
		}
	}

	return zombies, nil
}
