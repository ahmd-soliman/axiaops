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
};

export function serviceConfig(service) {
  return SERVICE_CONFIG[service] ?? { label: service.slice(0, 3), color: '#718096', bg: '#F7FAFC', darkBg: '#1A2030' };
}

// Display configuration for resource sub-types within a service.
// Used by the two-tier trend filter (e.g. EC2 → Instances / EBS Volumes / Snapshots).
export const RESOURCE_TYPE_CONFIG = {
  instance:         { label: 'Instances',   color: '#FF9900' },
  volume:           { label: 'EBS Volumes', color: '#E07B39' },
  snapshot:         { label: 'Snapshots',   color: '#D97706' },
  ami:              { label: 'AMIs',        color: '#F97316' },
  stopped_instance: { label: 'Stopped',     color: '#EF4444' },
  nat_gateway:      { label: 'NAT Gateway', color: '#10B981' },
  eip:              { label: 'Elastic IP',  color: '#14B8A6' },
};

export function resourceTypeConfig(rt) {
  return RESOURCE_TYPE_CONFIG[rt] ?? { label: rt, color: '#718096' };
}
