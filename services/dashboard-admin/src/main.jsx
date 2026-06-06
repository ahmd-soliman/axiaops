import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import App from './App.jsx';
import { AdminAuthProvider } from './auth/AdminAuth.jsx';
import { ThemeProvider } from './theme.jsx';
// Self-hosted Geist variable fonts + the AxiaOps design tokens, imported before
// any component so the brand palette is in place on first paint.
import '@fontsource-variable/geist';
import '@fontsource-variable/geist-mono';
import './tokens.css';
import './styles.css';

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <ThemeProvider>
      <BrowserRouter>
        <AdminAuthProvider>
          <App />
        </AdminAuthProvider>
      </BrowserRouter>
    </ThemeProvider>
  </React.StrictMode>,
);
