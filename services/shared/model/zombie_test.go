package model

import "testing"

func TestBuildARN(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		account  string
		region   string
		service  string
		resource string
		want     string
	}{
		// AWS tests
		{
			name:     "EC2 instance",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonEC2",
			resource: "i-1234567890abcdef0",
			want:     "arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0",
		},
		{
			name:     "EC2 EIP",
			provider: "aws",
			account:  "123456789012",
			region:   "eu-west-1",
			service:  "AmazonEC2",
			resource: "eipalloc-0123456789abcdef0",
			want:     "arn:aws:ec2:eu-west-1:123456789012:elastic-ip/eipalloc-0123456789abcdef0",
		},
		{
			name:     "EC2 volume",
			provider: "aws",
			account:  "123456789012",
			region:   "us-west-2",
			service:  "AmazonEC2",
			resource: "vol-0123456789abcdef0",
			want:     "arn:aws:ec2:us-west-2:123456789012:volume/vol-0123456789abcdef0",
		},
		{
			name:     "EC2 snapshot",
			provider: "aws",
			account:  "123456789012",
			region:   "ap-northeast-1",
			service:  "AmazonEC2",
			resource: "snap-0123456789abcdef0",
			want:     "arn:aws:ec2:ap-northeast-1:123456789012:snapshot/snap-0123456789abcdef0",
		},
		{
			name:     "VPC NAT gateway",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonVPC",
			resource: "nat-0123456789abcdef0",
			want:     "arn:aws:ec2:us-east-1:123456789012:natgateway/nat-0123456789abcdef0",
		},
		{
			name:     "VPC EIP",
			provider: "aws",
			account:  "123456789012",
			region:   "eu-west-1",
			service:  "AmazonVPC",
			resource: "eipalloc-0123456789abcdef0",
			want:     "arn:aws:ec2:eu-west-1:123456789012:elastic-ip/eipalloc-0123456789abcdef0",
		},
		{
			name:     "VPC",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonVPC",
			resource: "vpc-0123456789abcdef0",
			want:     "arn:aws:ec2:us-east-1:123456789012:vpc/vpc-0123456789abcdef0",
		},
		{
			name:     "RDS database",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonRDS",
			resource: "mydbinstance",
			want:     "arn:aws:rds:us-east-1:123456789012:db:mydbinstance",
		},
		{
			name:     "Lambda function",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AWSLambda",
			resource: "my-function",
			want:     "arn:aws:lambda:us-east-1:123456789012:function:my-function",
		},
		{
			name:     "ELB load balancer",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonElasticLoadBalancing",
			resource: "app/my-lb/1234567890abcdef",
			want:     "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-lb/1234567890abcdef",
		},
		{
			name:     "S3 bucket",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonS3",
			resource: "my-bucket",
			want:     "arn:aws:s3:us-east-1:123456789012:my-bucket",
		},
		{
			name:     "CloudFront distribution",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonCloudFront",
			resource: "E1234567890ABC",
			want:     "arn:aws:cloudfront:us-east-1:123456789012:distribution/E1234567890ABC",
		},
		{
			name:     "DynamoDB table",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonDynamoDB",
			resource: "my-table",
			want:     "arn:aws:dynamodb:us-east-1:123456789012:table/my-table",
		},
		{
			name:     "ElastiCache cluster",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonElastiCache",
			resource: "my-cluster",
			want:     "arn:aws:elasticache:us-east-1:123456789012:cluster:my-cluster",
		},
		{
			name:     "Redshift cluster",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonRedshift",
			resource: "my-cluster",
			want:     "arn:aws:redshift:us-east-1:123456789012:cluster:my-cluster",
		},
		{
			name:     "SNS topic",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonSNS",
			resource: "my-topic",
			want:     "arn:aws:sns:us-east-1:123456789012:my-topic",
		},
		{
			name:     "SNS topic with full ARN",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonSNS",
			resource: "arn:aws:sns:us-east-1:123456789012:my-topic",
			want:     "arn:aws:sns:us-east-1:123456789012:my-topic",
		},
		{
			name:     "SQS queue",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonSQS",
			resource: "my-queue",
			want:     "arn:aws:sqs:us-east-1:123456789012:my-queue",
		},
		{
			name:     "SQS queue with full ARN",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonSQS",
			resource: "arn:aws:sqs:us-east-1:123456789012:my-queue",
			want:     "arn:aws:sqs:us-east-1:123456789012:my-queue",
		},
		{
			name:     "ECS cluster",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonECS",
			resource: "my-cluster",
			want:     "arn:aws:ecs:us-east-1:123456789012:cluster/my-cluster",
		},
		{
			name:     "EKS cluster",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonEKS",
			resource: "my-cluster",
			want:     "arn:aws:eks:us-east-1:123456789012:cluster/my-cluster",
		},
		// Non-AWS tests
		{
			name:     "non-aws provider",
			provider: "gcp",
			account:  "my-project",
			region:   "us-central1",
			service:  "GoogleComputeEngine",
			resource: "my-instance",
			want:     "",
		},
		{
			name:     "unsupported service",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "UnsupportedService",
			resource: "resource-123",
			want:     "",
		},
		{
			name:     "unsupported resource type",
			provider: "aws",
			account:  "123456789012",
			region:   "us-east-1",
			service:  "AmazonEC2",
			resource: "unsupported-123",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildARN(tt.provider, tt.account, tt.region, tt.service, tt.resource)
			if got != tt.want {
				t.Errorf("BuildARN() = %q, want %q", got, tt.want)
			}
		})
	}
}
