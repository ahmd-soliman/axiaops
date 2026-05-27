// Runtime config — values injected by nginx envsubst at container startup.
// Falls back to build-time VITE_* vars for local development.
const env = window.__ENV__ || {};

export const DEV_MODE     = (env.DEV_MODE     ?? import.meta.env.VITE_DEV_MODE)     === 'true';
export const DEV_ORG_NAME = env.DEV_ORG_NAME  ?? import.meta.env.VITE_DEV_ORG_NAME  ?? 'AxiaOps Dev';

// AxiaOps' own AWS account ID — the principal that customer trust policies
// must allow on sts:AssumeRole. Production and staging get distinct values
// so dashboards rendered in different environments produce trust policies
// that point at the right place.
export const AXIAOPS_AWS_ACCOUNT_ID =
  env.AXIAOPS_AWS_ACCOUNT_ID ?? import.meta.env.VITE_AXIAOPS_AWS_ACCOUNT_ID ?? '';

// Public S3 URL of the AxiaOpsIntegrationRole CloudFormation template, surfaced
// by the aws-infra edge module (output onboarding_cfn_template_url). Powers the
// Connect screen's one-click "Launch Stack" button — the customer is deep-linked
// into CloudFormation with this template + their ExternalId pre-filled. Empty
// (e.g. local dev / self-hosted) hides the button; the manual JSON flow remains.
export const AXIAOPS_CFN_TEMPLATE_URL =
  env.AXIAOPS_CFN_TEMPLATE_URL ?? import.meta.env.VITE_AXIAOPS_CFN_TEMPLATE_URL ?? '';
