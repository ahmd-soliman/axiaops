// Default runtime env. In container deployments, inject-env.sh overwrites
// this file with the actual values at startup. In Vite dev (`npm run dev`)
// this default ships as-is — src/config.js falls back to import.meta.env
// for build-time VITE_* vars.
//
// Lives as an external file (not inline in index.html) so the strict
// `script-src 'self'` CSP in nginx.conf doesn't have to allow inline
// scripts.
window.__ENV__ = {};
