// Runtime config — values injected by nginx envsubst at container startup.
// Falls back to build-time VITE_* vars for local development.
const env = window.__ENV__ || {};

export const DEV_MODE     = (env.DEV_MODE     ?? import.meta.env.VITE_DEV_MODE)     === 'true';
export const DEV_ORG_NAME = env.DEV_ORG_NAME  ?? import.meta.env.VITE_DEV_ORG_NAME  ?? 'AxiaOps Dev';

// Build-time identifiers for the footer. These are tied to the bundle, not the
// deployment environment, so they live in build-time vars rather than the
// runtime window.__ENV__ injection. Defaults match the Dockerfile defaults so
// `vite dev` and `vite build` without env vars still produce a usable string.
export const APP_VERSION    = import.meta.env.VITE_APP_VERSION    || 'dev';
export const APP_COMMIT_SHA = import.meta.env.VITE_APP_COMMIT_SHA || 'local';

// Feature flags. Role-based AWS onboarding ships dark until staging end-to-end
// is proven (see docs/cross-account-roles-design.md §4). Set
// VITE_FEATURE_ROLE_AUTH=true to expose the "Role ARN (recommended)" tab on
// the Connect screen.
export const FEATURE_ROLE_AUTH =
  (env.FEATURE_ROLE_AUTH ?? import.meta.env.VITE_FEATURE_ROLE_AUTH) === 'true';

// AxiaOps' own AWS account ID — the principal that customer trust policies
// must allow on sts:AssumeRole. Production and staging get distinct values
// so dashboards rendered in different environments produce trust policies
// that point at the right place.
export const AXIAOPS_AWS_ACCOUNT_ID =
  env.AXIAOPS_AWS_ACCOUNT_ID ?? import.meta.env.VITE_AXIAOPS_AWS_ACCOUNT_ID ?? '';
