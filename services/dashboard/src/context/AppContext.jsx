import { createContext, useContext, useMemo } from 'react';

// Shared app-level state: orgName and logout handler.
// Consumed by AppShell and any screen that needs these values.
const AppContext = createContext(null);

export function AppProvider({ children, orgName, onLogout }) {
  // Memoize so consumers don't re-render when AuthenticatedApp re-renders for
  // unrelated reasons (onLogout/orgName are stable across those renders).
  const value = useMemo(() => ({ orgName, onLogout }), [orgName, onLogout]);
  return (
    <AppContext.Provider value={value}>
      {children}
    </AppContext.Provider>
  );
}

export function useApp() {
  const ctx = useContext(AppContext);
  if (!ctx) throw new Error('useApp must be used within AppProvider');
  return ctx;
}
