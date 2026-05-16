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
    setLoading(true);
    try {
      const data = await fetchMe();
      setMe(data);
      setError(null);
    } catch (err) {
      // /v1/me failure modes split three ways:
      //   • 403 — authenticated but no membership ("removed user").
      //     Set an empty-shaped me so consumers can render a removed-user
      //     screen or redirect; not a transient error.
      //   • 401 — definitive "not authenticated" answer (cookie missing,
      //     expired, revoked). Clear me so AuthGuard redirects to /login.
      //   • Anything else (429, 5xx, network) — transient. Keep the
      //     previous me so a momentary rate-limit or backend hiccup
      //     doesn't kick a still-authed user out of the app. Expose
      //     the error so the route guard can render a recoverable
      //     "couldn't reach API" fallback with a retry button.
      if (err.status === 403) {
        setMe({ user_id: '', organization_id: '', email: '', role: '', permissions: [] });
        setError(null);
      } else if (err.status === 401) {
        setMe(null);
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
