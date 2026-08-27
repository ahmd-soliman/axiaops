// Display configuration for AWS services — label, abbreviation, and brand colour.
// Each entry carries both a light-mode bg (soft pastel) and a dark-mode darkBg
// (deeply tinted equivalent) so chips look correct in both themes.
export const SERVICE_CONFIG = {
  AmazonEC2:                  { label: 'EC2', color: '#FF9900', bg: '#FFF7ED', darkBg: '#2A1D00' },
  AmazonRDS:                  { label: 'RDS', color: '#818CF8', bg: '#EEF2FF', darkBg: '#0D1140' },
  AWSLambda:                  { label: 'Lambda', color: '#F59E0B', bg: '#FFFBEB', darkBg: '#231800' },
  AmazonElasticLoadBalancing: { label: 'ELB', color: '#C4B5FD', bg: '#F5F3FF', darkBg: '#1A0D36' },
  AmazonVPC:                  { label: 'VPC', color: '#10B981', bg: '#ECFDF5', darkBg: '#042B1F' },
  AmazonEKS:                  { label: 'EKS', color: '#3B82F6', bg: '#EFF6FF', darkBg: '#0D1F40' },
  AWSCostExplorer:            { label: 'Cost Explorer', color: '#8B5CF6', bg: '#F5F3FF', darkBg: '#1A0D40' },
  AmazonS3:                   { label: 'S3',  color: '#06B6D4', bg: '#ECFEFF', darkBg: '#032830' },
  AmazonCloudFront:           { label: 'CloudFront',  color: '#EC4899', bg: '#FDF2F8', darkBg: '#2A0620' },
  AmazonCloudWatch:           { label: 'CloudWatch',  color: '#64748B', bg: '#F8FAFC', darkBg: '#1A2030' },
  AmazonECR:                  { label: 'ECR', color: '#EF4444', bg: '#FEF2F2', darkBg: '#3D0D0D' },
  AWSSecretsManager:          { label: 'Secrets', color: '#F97316', bg: '#FFFBF0', darkBg: '#3D1F0D' },
  AmazonKinesis:              { label: 'Kinesis', color: '#0D9488', bg: '#F0FDFA', darkBg: '#042F2A' },
  AWSGlue:                    { label: 'Glue', color: '#D946EF', bg: '#FDF4FF', darkBg: '#2D0A36' },
  AmazonSNS:                  { label: 'SNS', color: '#EAB308', bg: '#FEFCE8', darkBg: '#2A2010' },
  AmazonSQS:                  { label: 'SQS', color: '#84CC16', bg: '#F7FEE7', darkBg: '#1A2A0A' },
  AWSKms:                     { label: 'Key Management', color: '#F43F5E', bg: '#FFF1F2', darkBg: '#3D0A18' },
  AmazonGlacier:              { label: 'Glacier', color: '#0EA5E9', bg: '#F0F9FF', darkBg: '#0C2E3F' },
  AWSCloudFormation:          { label: 'CloudFormation', color: '#A855F7', bg: '#F9F5FF', darkBg: '#2D0A4D' },
  AWSDataTransfer:            { label: 'Data Transfer', color: '#4F46E5', bg: '#EEF2FF', darkBg: '#1A1140' },
  // Tier 2 detections (model.KnownServices). Added so the chip shows the real
  // service name instead of the `slice(0, 3)` fallback ("Ama..." for every
  // Amazon-prefixed service).
  AmazonDynamoDB:             { label: 'DynamoDB', color: '#1D4ED8', bg: '#DBEAFE', darkBg: '#172554' },
  AmazonElastiCache:          { label: 'ElastiCache', color: '#B91C1C', bg: '#FEE2E2', darkBg: '#450A0A' },
  AmazonES:                   { label: 'Elasticsearch', color: '#0E7490', bg: '#CFFAFE', darkBg: '#083344' },
  AmazonRedshift:             { label: 'Redshift', color: '#9F1239', bg: '#FFE4E6', darkBg: '#4C0519' },
  AmazonSageMaker:            { label: 'SageMaker', color: '#047857', bg: '#D1FAE5', darkBg: '#022C22' },
  AmazonECS:                  { label: 'ECS', color: '#C2410C', bg: '#FED7AA', darkBg: '#431407' },
  AmazonDocDB:                { label: 'DocDB', color: '#B91C1C', bg: '#FEE2E2', darkBg: '#450A0A' },
  AmazonMSK:                  { label: 'MSK', color: '#2563EB', bg: '#DBEAFE', darkBg: '#1E3A8A' },
  AmazonRoute53:              { label: 'Route53', color: '#059669', bg: '#D1FAE5', darkBg: '#064E3B' },
  AmazonBedrock:              { label: 'Bedrock', color: '#7C3AED', bg: '#EDE9FE', darkBg: '#3B0764' },
  AmazonKendra:               { label: 'Kendra', color: '#DB2777', bg: '#FCE7F3', darkBg: '#831843' },
  Tax:                        { label: 'Tax', color: '#94A3B8', bg: '#F8FAFC', darkBg: '#1E293B' },
};

export function serviceConfig(service) {
  if (SERVICE_CONFIG[service]) return SERVICE_CONFIG[service];
  // Fallback for services not yet explicitly styled: strip the Amazon/AWS
  // prefix so e.g. AmazonOpenSearchService renders as "OpenSearchService"
  // rather than getting truncated to "Ama" by a naive slice.
  const label = service.replace(/^(Amazon|AWS)/, '') || service;
  return { label, color: '#718096', bg: '#F7FAFC', darkBg: '#1A2030' };
}

// Display configuration for resource sub-types within a service.
// Used by the two-tier trend filter (e.g. EC2 → Instances / EBS Volumes / Snapshots).
export const RESOURCE_TYPE_CONFIG = {
  instance:         { label: 'Instances',      color: '#FF9900' },
  volume:           { label: 'EBS Volumes',    color: '#E07B39' },
  snapshot:         { label: 'Snapshots',      color: '#D97706' },
  ami:              { label: 'AMIs',           color: '#F97316' },
  stopped_instance: { label: 'Stopped',        color: '#EF4444' },
  nat_gateway:      { label: 'NAT Gateway',    color: '#10B981' },
  eip:              { label: 'Elastic IP',     color: '#14B8A6' },
  primary:          { label: 'Primary',        color: '#818CF8' },
  read_replica:     { label: 'Read Replica',   color: '#A78BFA' },
  classic:          { label: 'Classic LB',     color: '#C4B5FD' },
  alb:              { label: 'Application LB', color: '#DDD6FE' },
  nlb:              { label: 'Network LB',     color: '#EDE9FE' },
  ecs_service:        { label: 'ECS Service',        color: '#C2410C' },
  docdb_cluster:      { label: 'DocDB Cluster',      color: '#B91C1C' },
  msk_cluster:        { label: 'MSK Cluster',        color: '#2563EB' },
  route53_zone:       { label: 'Route53 Zone',       color: '#059669' },
  s3_multipart:       { label: 'Incomplete Uploads', color: '#06B6D4' },
  bedrock_throughput: { label: 'Bedrock Unit',       color: '#7C3AED' },
  kendra_index:       { label: 'Kendra Index',       color: '#DB2777' },
};

export function resourceTypeConfig(rt) {
  return RESOURCE_TYPE_CONFIG[rt] ?? { label: rt, color: '#718096' };
}

// Cost records carry no resource_type column — only a short resource ID
// (ingestion strips ARNs down to e.g. "nat-0abc123"). For services whose ID
// prefixes are unambiguous, the sub-type is derivable client-side, giving the
// cost screen the same two-tier filter the trend screen gets from snapshot
// data. IDs with no recognizable prefix (RDS names, ELB hashes) return null.
// Order matters: first match wins, so `i-` must stay last — any prefix added
// below it that also starts with "i" would be shadowed and classify as an
// EC2 instance.
const RESOURCE_ID_PREFIXES = [
  ['nat-',      'nat_gateway'],
  ['eipalloc-', 'eip'],
  ['vol-',      'volume'],
  ['snap-',     'snapshot'],
  ['ami-',      'ami'],
  ['i-',        'instance'],
];

export function resourceTypeFromId(resourceId) {
  if (!resourceId) return null;
  const match = RESOURCE_ID_PREFIXES.find(([prefix]) => resourceId.startsWith(prefix));
  return match ? match[1] : null;
}
