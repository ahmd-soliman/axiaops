// Package s3fixture implements a dev-only Provider that reads pre-seeded
// CostRecord JSON from an S3 bucket in LocalStack. It is used instead of the
// real AWS Cost Explorer when DEV_MODE=true, avoiding the need for real
// credentials or a LocalStack Pro subscription.
package s3fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"axiaops.io/ingestion/internal/model"
)

const (
	Bucket = "axiaops-fixtures"
	Key    = "costs.json"
)

// Client reads cost fixture data from S3 (LocalStack in dev).
type Client struct {
	s3 *s3.Client
}

func New(ctx context.Context) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("s3fixture: load config: %w", err)
	}
	return &Client{s3: s3.NewFromConfig(cfg)}, nil
}

func (c *Client) Name() string { return "s3fixture" }

// FetchCosts downloads costs.json from S3 and returns the records.
// The start/end parameters are accepted to satisfy the Provider interface
// but are ignored — the fixture returns all records as-is.
func (c *Client) FetchCosts(ctx context.Context, _, _ time.Time) ([]model.CostRecord, error) {
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(Bucket),
		Key:    aws.String(Key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3fixture: GetObject: %w", err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("s3fixture: read body: %w", err)
	}

	var records []model.CostRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("s3fixture: unmarshal: %w", err)
	}

	return records, nil
}
