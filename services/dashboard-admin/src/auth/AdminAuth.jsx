import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { adminApi, ApiError } from '../api/admin.js';

// AdminAuthContext holds the resolved staff principal (or null). On mount it
// probes /admin/me to rehydrate an existing session cookie, so a page refresh
// doesn't bounce a logged-in staff member back to the login screen.
const AdminAuthContext = createContext(null);

export function AdminAuthProvider({ children }) {
  const [staff, setStaff] = useState(null);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    try {
      const me = await adminApi.me();
      setStaff(me);
    } catch (err) {
      // Any failure (401 no-session, or a malformed/HTML error body from a
      // misconfigured ingress → SyntaxError) lands the user on the login
      // screen. Never re-throw here: this runs in a useEffect where a rejected
      // promise is unhandled and would leave `loading` stuck true forever.
      setStaff(null);
      if (!(err instanceof ApiError)) {
        console.error('admin: session probe failed', err);
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const login = useCallback(async (email, password) => {
    const me = await adminApi.login(email, password);
    setStaff(me);
    return me;
  }, []);

  const logout = useCallback(async () => {
    try {
      await adminApi.logout();
    } catch (err) {
      // The UI logs out regardless; swallow a failed logout request (e.g. a
      // network blip) so the click handler doesn't leave an unhandled
      // rejection.
      console.error('admin: logout request failed', err);
    } finally {
      setStaff(null);
    }
  }, []);

  const value = useMemo(
    () => ({ staff, loading, login, logout, refresh }),
    [staff, loading, login, logout, refresh],
  );
  return <AdminAuthContext.Provider value={value}>{children}</AdminAuthContext.Provider>;
}

export function useAdminAuth() {
  const ctx = useContext(AdminAuthContext);
  if (!ctx) throw new Error('useAdminAuth must be used within AdminAuthProvider');
  return ctx;
}

// hasRole — superadmin is treated as sufficient for any role check, mirroring
// the backend's RequireRole semantics.
export function hasRole(staff, role) {
  if (!staff?.roles) return false;
  return staff.roles.includes('superadmin') || staff.roles.includes(role);
}
