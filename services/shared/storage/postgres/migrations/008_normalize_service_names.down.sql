-- Rollback: restore original Cost Explorer service names
-- This reverts the normalization applied in the up migration

BEGIN;

-- Revert cost_records
UPDATE axiaops.cost_records SET service = 'Amazon Elastic Compute Cloud - Compute'
  WHERE service = 'AmazonEC2';
UPDATE axiaops.cost_records SET service = 'Amazon Relational Database Service'
  WHERE service = 'AmazonRDS';
UPDATE axiaops.cost_records SET service = 'AWS Lambda'
  WHERE service = 'AWSLambda';
UPDATE axiaops.cost_records SET service = 'Amazon Elastic Load Balancing'
  WHERE service = 'AmazonElasticLoadBalancing';
UPDATE axiaops.cost_records SET service = 'Amazon Virtual Private Cloud'
  WHERE service = 'AmazonVPC';
UPDATE axiaops.cost_records SET service = 'Amazon ElastiCache'
  WHERE service = 'AmazonElastiCache';
UPDATE axiaops.cost_records SET service = 'Amazon OpenSearch Service'
  WHERE service = 'AmazonES';
UPDATE axiaops.cost_records SET service = 'Amazon Redshift'
  WHERE service = 'AmazonRedshift';
UPDATE axiaops.cost_records SET service = 'Amazon SageMaker'
  WHERE service = 'AmazonSageMaker';
UPDATE axiaops.cost_records SET service = 'Amazon DynamoDB'
  WHERE service = 'AmazonDynamoDB';
UPDATE axiaops.cost_records SET service = 'Amazon Elastic Kubernetes Service'
  WHERE service = 'AmazonEKS';
UPDATE axiaops.cost_records SET service = 'Amazon Cost Explorer'
  WHERE service = 'AWSCostExplorer';
UPDATE axiaops.cost_records SET service = 'Amazon CloudWatch'
  WHERE service = 'AmazonCloudWatch';

-- Revert ghost_records
UPDATE axiaops.ghost_records SET service = 'Amazon Elastic Compute Cloud - Compute'
  WHERE service = 'AmazonEC2';
UPDATE axiaops.ghost_records SET service = 'Amazon Relational Database Service'
  WHERE service = 'AmazonRDS';
UPDATE axiaops.ghost_records SET service = 'AWS Lambda'
  WHERE service = 'AWSLambda';
UPDATE axiaops.ghost_records SET service = 'Amazon Elastic Load Balancing'
  WHERE service = 'AmazonElasticLoadBalancing';
UPDATE axiaops.ghost_records SET service = 'Amazon Virtual Private Cloud'
  WHERE service = 'AmazonVPC';
UPDATE axiaops.ghost_records SET service = 'Amazon ElastiCache'
  WHERE service = 'AmazonElastiCache';
UPDATE axiaops.ghost_records SET service = 'Amazon OpenSearch Service'
  WHERE service = 'AmazonES';
UPDATE axiaops.ghost_records SET service = 'Amazon Redshift'
  WHERE service = 'AmazonRedshift';
UPDATE axiaops.ghost_records SET service = 'Amazon SageMaker'
  WHERE service = 'AmazonSageMaker';
UPDATE axiaops.ghost_records SET service = 'Amazon DynamoDB'
  WHERE service = 'AmazonDynamoDB';
UPDATE axiaops.ghost_records SET service = 'Amazon Elastic Kubernetes Service'
  WHERE service = 'AmazonEKS';
UPDATE axiaops.ghost_records SET service = 'Amazon Cost Explorer'
  WHERE service = 'AWSCostExplorer';
UPDATE axiaops.ghost_records SET service = 'Amazon CloudWatch'
  WHERE service = 'AmazonCloudWatch';

-- Revert resource_records
UPDATE axiaops.resource_records SET service = 'Amazon Elastic Compute Cloud - Compute'
  WHERE service = 'AmazonEC2';
UPDATE axiaops.resource_records SET service = 'Amazon Relational Database Service'
  WHERE service = 'AmazonRDS';
UPDATE axiaops.resource_records SET service = 'AWS Lambda'
  WHERE service = 'AWSLambda';
UPDATE axiaops.resource_records SET service = 'Amazon Elastic Load Balancing'
  WHERE service = 'AmazonElasticLoadBalancing';
UPDATE axiaops.resource_records SET service = 'Amazon Virtual Private Cloud'
  WHERE service = 'AmazonVPC';
UPDATE axiaops.resource_records SET service = 'Amazon ElastiCache'
  WHERE service = 'AmazonElastiCache';
UPDATE axiaops.resource_records SET service = 'Amazon OpenSearch Service'
  WHERE service = 'AmazonES';
UPDATE axiaops.resource_records SET service = 'Amazon Redshift'
  WHERE service = 'AmazonRedshift';
UPDATE axiaops.resource_records SET service = 'Amazon SageMaker'
  WHERE service = 'AmazonSageMaker';
UPDATE axiaops.resource_records SET service = 'Amazon DynamoDB'
  WHERE service = 'AmazonDynamoDB';
UPDATE axiaops.resource_records SET service = 'Amazon Elastic Kubernetes Service'
  WHERE service = 'AmazonEKS';
UPDATE axiaops.resource_records SET service = 'Amazon Cost Explorer'
  WHERE service = 'AWSCostExplorer';
UPDATE axiaops.resource_records SET service = 'Amazon CloudWatch'
  WHERE service = 'AmazonCloudWatch';
UPDATE axiaops.resource_records SET service = 'Amazon Simple Storage Service'
  WHERE service = 'AmazonS3';
UPDATE axiaops.resource_records SET service = 'AWS Glue'
  WHERE service = 'AWSGlue';
UPDATE axiaops.resource_records SET service = 'Amazon Simple Notification Service'
  WHERE service = 'AmazonSNS';
UPDATE axiaops.resource_records SET service = 'Amazon Simple Queue Service'
  WHERE service = 'AmazonSQS';
UPDATE axiaops.resource_records SET service = 'AWS Secrets Manager'
  WHERE service = 'AWSSecretsManager';
UPDATE axiaops.resource_records SET service = 'AWS Key Management Service'
  WHERE service = 'AWSKms';
UPDATE axiaops.resource_records SET service = 'Amazon Glacier'
  WHERE service = 'AmazonGlacier';
UPDATE axiaops.resource_records SET service = 'AWS CloudFormation'
  WHERE service = 'AWSCloudFormation';

-- Revert cost_records
UPDATE axiaops.cost_records SET service = 'Amazon Simple Storage Service'
  WHERE service = 'AmazonS3';
UPDATE axiaops.cost_records SET service = 'AWS Glue'
  WHERE service = 'AWSGlue';
UPDATE axiaops.cost_records SET service = 'Amazon Simple Notification Service'
  WHERE service = 'AmazonSNS';
UPDATE axiaops.cost_records SET service = 'Amazon Simple Queue Service'
  WHERE service = 'AmazonSQS';
UPDATE axiaops.cost_records SET service = 'AWS Secrets Manager'
  WHERE service = 'AWSSecretsManager';
UPDATE axiaops.cost_records SET service = 'AWS Key Management Service'
  WHERE service = 'AWSKms';
UPDATE axiaops.cost_records SET service = 'Amazon Glacier'
  WHERE service = 'AmazonGlacier';
UPDATE axiaops.cost_records SET service = 'AWS CloudFormation'
  WHERE service = 'AWSCloudFormation';

-- Revert ghost_records
UPDATE axiaops.ghost_records SET service = 'Amazon Simple Storage Service'
  WHERE service = 'AmazonS3';
UPDATE axiaops.ghost_records SET service = 'AWS Glue'
  WHERE service = 'AWSGlue';
UPDATE axiaops.ghost_records SET service = 'Amazon Simple Notification Service'
  WHERE service = 'AmazonSNS';
UPDATE axiaops.ghost_records SET service = 'Amazon Simple Queue Service'
  WHERE service = 'AmazonSQS';
UPDATE axiaops.ghost_records SET service = 'AWS Secrets Manager'
  WHERE service = 'AWSSecretsManager';
UPDATE axiaops.ghost_records SET service = 'AWS Key Management Service'
  WHERE service = 'AWSKms';
UPDATE axiaops.ghost_records SET service = 'Amazon Glacier'
  WHERE service = 'AmazonGlacier';
UPDATE axiaops.ghost_records SET service = 'AWS CloudFormation'
  WHERE service = 'AWSCloudFormation';

COMMIT;
