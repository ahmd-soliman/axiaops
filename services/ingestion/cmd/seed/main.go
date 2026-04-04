// seed uploads the cost fixture file to LocalStack S3.
// Run once after LocalStack starts: go run ./cmd/seed
package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"axiaops.io/ingestion/internal/provider/s3fixture"
)

const fixtureFile = "fixtures/costs.json"

func main() {
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("seed: load config: %v", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true // required for LocalStack
	})

	// Create bucket
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(s3fixture.Bucket),
	})
	if err != nil {
		log.Printf("seed: bucket may already exist: %v", err)
	}

	// Upload fixture
	f, err := os.Open(fixtureFile)
	if err != nil {
		log.Fatalf("seed: open fixture: %v", err)
	}
	defer f.Close()

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s3fixture.Bucket),
		Key:    aws.String(s3fixture.Key),
		Body:   f,
	})
	if err != nil {
		log.Fatalf("seed: PutObject: %v", err)
	}

	log.Printf("seed: uploaded %s to s3://%s/%s", fixtureFile, s3fixture.Bucket, s3fixture.Key)
}
