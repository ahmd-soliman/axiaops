// Display configuration for AWS services — label, abbreviation, and brand colour.
export const SERVICE_CONFIG = {
  AmazonEC2: { label: 'EC2', color: '#FF9900', bg: '#FFF7ED' },
  AmazonRDS: { label: 'RDS', color: '#3B48CC', bg: '#EEF2FF' },
  AWSLambda: { label: 'λ', color: '#F59E0B', bg: '#FFFBEB' },
  AmazonElasticLoadBalancing: { label: 'ELB', color: '#8B5CF6', bg: '#F5F3FF' },
  AmazonVPC: { label: 'VPC', color: '#10B981', bg: '#ECFDF5' },
  AmazonS3: { label: 'S3', color: '#06B6D4', bg: '#ECFEFF' },
  AmazonCloudFront: { label: 'CF', color: '#EC4899', bg: '#FDF2F8' },
  AmazonCloudWatch: { label: 'CW', color: '#64748B', bg: '#F8FAFC' },
};

export function serviceConfig(service) {
  return SERVICE_CONFIG[service] ?? { label: service.slice(0, 3), color: '#718096', bg: '#F7FAFC' };
}
