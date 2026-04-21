import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    // Respect VITE_NO_OPEN=true to suppress auto-open (used by VS Code debug
    // tasks, where Chrome is launched separately by the debugger).
    open: process.env.VITE_NO_OPEN !== 'true',
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
    },
  },
});
