// Runtime config — values injected by nginx envsubst at container startup.
// Falls back to build-time VITE_* vars for local development.
const env = window.__ENV__ || {};

export const DEV_MODE        = (env.DEV_MODE        ?? import.meta.env.VITE_DEV_MODE)        === 'true';
export const KINDE_ISSUER    = env.KINDE_ISSUER      ?? import.meta.env.VITE_KINDE_ISSUER    ?? '';
export const KINDE_CLIENT_ID = env.KINDE_CLIENT_ID   ?? import.meta.env.VITE_KINDE_CLIENT_ID ?? '';
export const DEV_ORG_NAME    = env.DEV_ORG_NAME      ?? import.meta.env.VITE_DEV_ORG_NAME    ?? 'AxiaOps Dev';

// Build-time identifiers for the footer. These are tied to the bundle, not the
// deployment environment, so they live in build-time vars rather than the
// runtime window.__ENV__ injection. Defaults match the Dockerfile defaults so
// `vite dev` and `vite build` without env vars still produce a usable string.
export const APP_VERSION    = import.meta.env.VITE_APP_VERSION    || 'dev';
export const APP_COMMIT_SHA = import.meta.env.VITE_APP_COMMIT_SHA || 'local';
