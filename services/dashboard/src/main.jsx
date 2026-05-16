import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ThemeProvider } from './theme/ThemeContext';
import { ToastProvider } from './context/ToastContext';
import App from './App';
// Self-hosted variable fonts — bundled into the build, no runtime CDN
// dependency so the dashboard renders correctly in offline / on-prem /
// air-gapped environments.
import '@fontsource-variable/geist';
import '@fontsource-variable/geist-mono';
// Design tokens — :root CSS custom properties. Imported before any
// component so the cascade is in place on the very first render
// (issue #88).
import './styles/tokens.css';
import './index.css';

export const queryClient = new QueryClient();

ReactDOM.createRoot(document.getElementById('root')).render(
  <QueryClientProvider client={queryClient}>
    <ThemeProvider>
      <ToastProvider>
        <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
          <App />
        </BrowserRouter>
      </ToastProvider>
    </ThemeProvider>
  </QueryClientProvider>
);
