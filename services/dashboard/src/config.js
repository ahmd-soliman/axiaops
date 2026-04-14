// Runtime config — values injected by nginx envsubst at container startup.
// Falls back to build-time EXPO_PUBLIC_* vars for local development.
const env = window.__ENV__ || {};

export const DEV_MODE      = (env.DEV_MODE      ?? process.env.EXPO_PUBLIC_DEV_MODE)      === 'true';
export const KINDE_ISSUER  = env.KINDE_ISSUER   ?? process.env.EXPO_PUBLIC_KINDE_ISSUER   ?? '';
export const KINDE_CLIENT_ID = env.KINDE_CLIENT_ID ?? process.env.EXPO_PUBLIC_KINDE_CLIENT_ID ?? '';
export const DEV_ORG_NAME  = env.DEV_ORG_NAME   ?? process.env.EXPO_PUBLIC_DEV_ORG_NAME   ?? 'AxiaOps Dev';
