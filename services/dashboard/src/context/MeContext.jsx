import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { fetchMe, FORBIDDEN_EVENT } from '../api/client';

// MeContext exposes the authenticated user's role + permissions, plus a
// helper `can(perm)` for UI gating. The provider auto-fetches once on mount
// and refreshes whenever any API call returns 403 (the data layer dispatches
// FORBIDDEN_EVENT for that). See docs/rbac-design.md §8 "Role-change propagation".
const MeContext = createContext(null);

export function MeProvider({ children }) {
  const [me, setMe] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  const refresh = useCallback(async () => {
    try {
      const data = await fetchMe();
      setMe(data);
      setError(null);
    } catch (err) {
      // 403 on /v1/me itself means: authenticated, but no membership.
      // The dashboard treats this as "removed user" — the consumer can
      // redirect to /login or render a removed-user screen.
      if (err.status === 403) {
        setMe({ user_id: '', tenant_id: '', email: '', role: '', permissions: [] });
        setError(null);
      } else {
        setError(err);
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Re-fetch whenever any API call hits 403. Debouncing isn't worth the
  // complexity at the rates we'll see — a click that 403s is followed by
  // exactly one /v1/me call.
  useEffect(() => {
    const handler = () => { refresh(); };
    window.addEventListener(FORBIDDEN_EVENT, handler);
    return () => window.removeEventListener(FORBIDDEN_EVENT, handler);
  }, [refresh]);

  const value = useMemo(() => {
    const permissions = me?.permissions || [];
    const permSet = new Set(permissions);
    return {
      me,
      loading,
      error,
      refresh,
      role: me?.role || '',
      permissions,
      can: (perm) => permSet.has(perm),
    };
  }, [me, loading, error, refresh]);

  return <MeContext.Provider value={value}>{children}</MeContext.Provider>;
}

export function useMe() {
  const ctx = useContext(MeContext);
  if (!ctx) throw new Error('useMe must be used within MeProvider');
  return ctx;
}
