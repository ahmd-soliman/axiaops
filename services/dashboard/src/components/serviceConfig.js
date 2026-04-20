// Display configuration for AWS services — label, abbreviation, and brand colour.
// Each entry carries both a light-mode bg (soft pastel) and a dark-mode darkBg
// (deeply tinted equivalent) so chips look correct in both themes.
export const SERVICE_CONFIG = {
  AmazonEC2:                  { label: 'EC2', color: '#FF9900', bg: '#FFF7ED', darkBg: '#2A1D00' },
  AmazonRDS:                  { label: 'RDS', color: '#818CF8', bg: '#EEF2FF', darkBg: '#0D1140' },
  AWSLambda:                  { label: 'λ',   color: '#F59E0B', bg: '#FFFBEB', darkBg: '#231800' },
  AmazonElasticLoadBalancing: { label: 'ELB', color: '#C4B5FD', bg: '#F5F3FF', darkBg: '#1A0D36' },
  AmazonVPC:                  { label: 'VPC', color: '#10B981', bg: '#ECFDF5', darkBg: '#042B1F' },
  AmazonEKS:                  { label: 'EKS', color: '#3B82F6', bg: '#EFF6FF', darkBg: '#0D1F40' },
  AWSCostExplorer:            { label: 'CE',  color: '#8B5CF6', bg: '#F5F3FF', darkBg: '#1A0D40' },
  AmazonS3:                   { label: 'S3',  color: '#06B6D4', bg: '#ECFEFF', darkBg: '#032830' },
  AmazonCloudFront:           { label: 'CF',  color: '#EC4899', bg: '#FDF2F8', darkBg: '#2A0620' },
  AmazonCloudWatch:           { label: 'CW',  color: '#64748B', bg: '#F8FAFC', darkBg: '#1A2030' },
  AmazonECR:                  { label: 'ECR', color: '#EF4444', bg: '#FEF2F2', darkBg: '#3D0D0D' },
  AWSSecretsManager:          { label: 'Secrets', color: '#F97316', bg: '#FFFBF0', darkBg: '#3D1F0D' },
  AmazonKinesis:              { label: 'Kinesis', color: '#06B6D4', bg: '#ECFEFF', darkBg: '#032830' },
  AWSGlue:                    { label: 'Glue', color: '#8B5CF6', bg: '#F5F3FF', darkBg: '#1A0D40' },
  AmazonSNS:                  { label: 'SNS', color: '#F59E0B', bg: '#FFFBEB', darkBg: '#231800' },
  AmazonSQS:                  { label: 'SQS', color: '#10B981', bg: '#ECFDF5', darkBg: '#042B1F' },
  AWSKms:                     { label: 'KMS', color: '#EC4899', bg: '#FDF2F8', darkBg: '#2A0620' },
  AmazonGlacier:              { label: 'Glacier', color: '#0EA5E9', bg: '#F0F9FF', darkBg: '#0C2E3F' },
  AWSCloudFormation:          { label: 'CFN', color: '#A855F7', bg: '#F9F5FF', darkBg: '#2D0A4D' },
};

export function serviceConfig(service) {
  return SERVICE_CONFIG[service] ?? { label: service.slice(0, 3), color: '#718096', bg: '#F7FAFC', darkBg: '#1A2030' };
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
};

export function resourceTypeConfig(rt) {
  return RESOURCE_TYPE_CONFIG[rt] ?? { label: rt, color: '#718096' };
}
