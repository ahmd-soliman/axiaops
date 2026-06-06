import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

// The admin UI is a standalone app on its own port (5174) so it never shares a
// bundle — or a running dev server — with the tenant dashboard (5173). In dev
// it proxies /admin/* to the admin-plane binary (cmd/api-admin) on :8090 with
// NO path rewrite (the backend routes are literally /admin/*), which keeps the
// browser same-origin so the axiaops_staff_session cookie round-trips without
// any CORS dance — mirroring how services/dashboard proxies /api to :8080.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5174,
    open: process.env.VITE_NO_OPEN !== 'true',
    proxy: {
      '/admin': {
        target: 'http://localhost:8090',
        // Rewrite the Host header to the target so loopback cross-port proxying
        // is robust if the backend (or a future ingress) ever validates Host.
        changeOrigin: true,
      },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.js'],
  },
});
