-- Normalize Cost Explorer service names to internal names in existing records
-- This ensures old data is consistent with the new normalization in the ingestion service

BEGIN;

-- Update cost_records with the mapping
UPDATE axiaops.cost_records SET service = 'AmazonEC2'
  WHERE service = 'Amazon Elastic Compute Cloud - Compute';
UPDATE axiaops.cost_records SET service = 'AmazonRDS'
  WHERE service = 'Amazon Relational Database Service';
UPDATE axiaops.cost_records SET service = 'AWSLambda'
  WHERE service = 'AWS Lambda';
UPDATE axiaops.cost_records SET service = 'AmazonElasticLoadBalancing'
  WHERE service = 'Amazon Elastic Load Balancing';
UPDATE axiaops.cost_records SET service = 'AmazonVPC'
  WHERE service = 'Amazon Virtual Private Cloud';
UPDATE axiaops.cost_records SET service = 'AmazonElastiCache'
  WHERE service = 'Amazon ElastiCache';
UPDATE axiaops.cost_records SET service = 'AmazonES'
  WHERE service = 'Amazon OpenSearch Service';
UPDATE axiaops.cost_records SET service = 'AmazonRedshift'
  WHERE service = 'Amazon Redshift';
UPDATE axiaops.cost_records SET service = 'AmazonSageMaker'
  WHERE service = 'Amazon SageMaker';
UPDATE axiaops.cost_records SET service = 'AmazonDynamoDB'
  WHERE service = 'Amazon DynamoDB';
UPDATE axiaops.cost_records SET service = 'AmazonEKS'
  WHERE service = 'Amazon Elastic Kubernetes Service';
UPDATE axiaops.cost_records SET service = 'AWSCostExplorer'
  WHERE service = 'Amazon Cost Explorer';
UPDATE axiaops.cost_records SET service = 'AmazonCloudWatch'
  WHERE service = 'Amazon CloudWatch';

-- Update ghost_records with the same mapping
UPDATE axiaops.ghost_records SET service = 'AmazonEC2'
  WHERE service = 'Amazon Elastic Compute Cloud - Compute';
UPDATE axiaops.ghost_records SET service = 'AmazonRDS'
  WHERE service = 'Amazon Relational Database Service';
UPDATE axiaops.ghost_records SET service = 'AWSLambda'
  WHERE service = 'AWS Lambda';
UPDATE axiaops.ghost_records SET service = 'AmazonElasticLoadBalancing'
  WHERE service = 'Amazon Elastic Load Balancing';
UPDATE axiaops.ghost_records SET service = 'AmazonVPC'
  WHERE service = 'Amazon Virtual Private Cloud';
UPDATE axiaops.ghost_records SET service = 'AmazonElastiCache'
  WHERE service = 'Amazon ElastiCache';
UPDATE axiaops.ghost_records SET service = 'AmazonES'
  WHERE service = 'Amazon OpenSearch Service';
UPDATE axiaops.ghost_records SET service = 'AmazonRedshift'
  WHERE service = 'Amazon Redshift';
UPDATE axiaops.ghost_records SET service = 'AmazonSageMaker'
  WHERE service = 'Amazon SageMaker';
UPDATE axiaops.ghost_records SET service = 'AmazonDynamoDB'
  WHERE service = 'Amazon DynamoDB';
UPDATE axiaops.ghost_records SET service = 'AmazonEKS'
  WHERE service = 'Amazon Elastic Kubernetes Service';
UPDATE axiaops.ghost_records SET service = 'AWSCostExplorer'
  WHERE service = 'Amazon Cost Explorer';
UPDATE axiaops.ghost_records SET service = 'AmazonCloudWatch'
  WHERE service = 'Amazon CloudWatch';

-- Update resource_records with the same mapping
UPDATE axiaops.resource_records SET service = 'AmazonEC2'
  WHERE service = 'Amazon Elastic Compute Cloud - Compute';
UPDATE axiaops.resource_records SET service = 'AmazonRDS'
  WHERE service = 'Amazon Relational Database Service';
UPDATE axiaops.resource_records SET service = 'AWSLambda'
  WHERE service = 'AWS Lambda';
UPDATE axiaops.resource_records SET service = 'AmazonElasticLoadBalancing'
  WHERE service = 'Amazon Elastic Load Balancing';
UPDATE axiaops.resource_records SET service = 'AmazonVPC'
  WHERE service = 'Amazon Virtual Private Cloud';
UPDATE axiaops.resource_records SET service = 'AmazonElastiCache'
  WHERE service = 'Amazon ElastiCache';
UPDATE axiaops.resource_records SET service = 'AmazonES'
  WHERE service = 'Amazon OpenSearch Service';
UPDATE axiaops.resource_records SET service = 'AmazonRedshift'
  WHERE service = 'Amazon Redshift';
UPDATE axiaops.resource_records SET service = 'AmazonSageMaker'
  WHERE service = 'Amazon SageMaker';
UPDATE axiaops.resource_records SET service = 'AmazonDynamoDB'
  WHERE service = 'Amazon DynamoDB';
UPDATE axiaops.resource_records SET service = 'AmazonEKS'
  WHERE service = 'Amazon Elastic Kubernetes Service';
UPDATE axiaops.resource_records SET service = 'AWSCostExplorer'
  WHERE service = 'Amazon Cost Explorer';
UPDATE axiaops.resource_records SET service = 'AmazonCloudWatch'
  WHERE service = 'Amazon CloudWatch';
UPDATE axiaops.resource_records SET service = 'AmazonS3'
  WHERE service = 'Amazon Simple Storage Service';
UPDATE axiaops.resource_records SET service = 'AWSGlue'
  WHERE service = 'AWS Glue';
UPDATE axiaops.resource_records SET service = 'AmazonSNS'
  WHERE service = 'Amazon Simple Notification Service';
UPDATE axiaops.resource_records SET service = 'AmazonSQS'
  WHERE service = 'Amazon Simple Queue Service';
UPDATE axiaops.resource_records SET service = 'AWSSecretsManager'
  WHERE service = 'AWS Secrets Manager';
UPDATE axiaops.resource_records SET service = 'AWSKms'
  WHERE service = 'AWS Key Management Service';
UPDATE axiaops.resource_records SET service = 'AmazonGlacier'
  WHERE service = 'Amazon Glacier';
UPDATE axiaops.resource_records SET service = 'AWSCloudFormation'
  WHERE service = 'AWS CloudFormation';

-- Update cost_records with the same mapping
UPDATE axiaops.cost_records SET service = 'AmazonS3'
  WHERE service = 'Amazon Simple Storage Service';
UPDATE axiaops.cost_records SET service = 'AWSGlue'
  WHERE service = 'AWS Glue';
UPDATE axiaops.cost_records SET service = 'AmazonSNS'
  WHERE service = 'Amazon Simple Notification Service';
UPDATE axiaops.cost_records SET service = 'AmazonSQS'
  WHERE service = 'Amazon Simple Queue Service';
UPDATE axiaops.cost_records SET service = 'AWSSecretsManager'
  WHERE service = 'AWS Secrets Manager';
UPDATE axiaops.cost_records SET service = 'AWSKms'
  WHERE service = 'AWS Key Management Service';
UPDATE axiaops.cost_records SET service = 'AmazonGlacier'
  WHERE service = 'Amazon Glacier';
UPDATE axiaops.cost_records SET service = 'AWSCloudFormation'
  WHERE service = 'AWS CloudFormation';

-- Update ghost_records with the same mapping
UPDATE axiaops.ghost_records SET service = 'AmazonS3'
  WHERE service = 'Amazon Simple Storage Service';
UPDATE axiaops.ghost_records SET service = 'AWSGlue'
  WHERE service = 'AWS Glue';
UPDATE axiaops.ghost_records SET service = 'AmazonSNS'
  WHERE service = 'Amazon Simple Notification Service';
UPDATE axiaops.ghost_records SET service = 'AmazonSQS'
  WHERE service = 'Amazon Simple Queue Service';
UPDATE axiaops.ghost_records SET service = 'AWSSecretsManager'
  WHERE service = 'AWS Secrets Manager';
UPDATE axiaops.ghost_records SET service = 'AWSKms'
  WHERE service = 'AWS Key Management Service';
UPDATE axiaops.ghost_records SET service = 'AmazonGlacier'
  WHERE service = 'Amazon Glacier';
UPDATE axiaops.ghost_records SET service = 'AWSCloudFormation'
  WHERE service = 'AWS CloudFormation';

COMMIT;
