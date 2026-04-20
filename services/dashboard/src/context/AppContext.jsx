import { createContext, useContext } from 'react';

// Shared app-level state: orgName and logout handler.
// Consumed by AppShell and any screen that needs these values.
const AppContext = createContext(null);

export function AppProvider({ children, orgName, onLogout }) {
  return (
    <AppContext.Provider value={{ orgName, onLogout }}>
      {children}
    </AppContext.Provider>
  );
}

export function useApp() {
  const ctx = useContext(AppContext);
  if (!ctx) throw new Error('useApp must be used within AppProvider');
  return ctx;
}
