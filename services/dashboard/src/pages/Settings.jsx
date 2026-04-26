import { Navigate, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { useTheme } from '../theme/ThemeContext';
import { useMe } from '../context/MeContext';
import { PERM } from '../api/permissions';

// Settings hub: vertical sub-nav (left) + active tab pane (right).
// Tabs are gated by permission; tabs the caller can't see don't render.
//
// Sub-nav fits the SaaS standard (Stripe, Linear, GitHub, Vercel) — keeps
// configuration grouped and out of the top nav, and lets new admin pages
// land here without crowding daily-use routes.

const TABS = [
  { label: 'Team',      path: '/settings/team',      requires: PERM.MEMBERS_INVITE },
  { label: 'Audit log', path: '/settings/audit',     requires: PERM.AUDIT_READ },
  { label: 'Workspace', path: '/settings/workspace', requires: PERM.TENANT_DELETE },
];

export default function Settings() {
  const { theme: t, isDark } = useTheme();
  const { can, loading } = useMe();
  const location = useLocation();
  const navigate = useNavigate();

  // Wait for /v1/me before deciding what to render. On first paint
  // `loading` is true and every `can()` returns false — without this gate
  // the user sees the empty-state flash before the redirect can fire.
  if (loading) return null;

  const visible = TABS.filter((tab) => can(tab.requires));

  // Land on first visible tab. <Navigate> is the declarative form;
  // imperative navigate() during render fights React's render cycle.
  if (
    (location.pathname === '/settings' || location.pathname === '/settings/') &&
    visible.length > 0
  ) {
    return <Navigate to={visible[0].path} replace />;
  }

  return (
    <div style={{ display: 'flex', minHeight: '100%', backgroundColor: t.bg }}>
      <aside
        style={{
          width: 220,
          flexShrink: 0,
          borderRight: `1px solid ${t.border}`,
          backgroundColor: t.surface,
          padding: '24px 12px',
        }}
      >
        <h2
          style={{
            margin: '0 8px 12px',
            fontSize: 11,
            fontWeight: 700,
            letterSpacing: 0.5,
            textTransform: 'uppercase',
            color: t.textMuted,
          }}
        >
          Settings
        </h2>
        <nav>
          {visible.map((tab) => {
            const active = location.pathname.startsWith(tab.path);
            return (
              <button
                key={tab.path}
                type="button"
                onClick={() => navigate(tab.path)}
                aria-current={active ? 'page' : undefined}
                style={{
                  display: 'block',
                  width: '100%',
                  textAlign: 'left',
                  padding: '8px 10px',
                  marginBottom: 2,
                  borderRadius: 6,
                  border: 'none',
                  backgroundColor: active ? (isDark ? 'rgba(255,255,255,0.05)' : t.accentLight) : 'transparent',
                  color: active ? t.accent : t.text,
                  fontSize: 13,
                  fontWeight: active ? 700 : 500,
                  cursor: 'pointer',
                }}
              >
                {tab.label}
              </button>
            );
          })}
        </nav>
      </aside>
      <main style={{ flex: 1, minWidth: 0 }}>
        {visible.length === 0 ? (
          <div style={{ padding: 24, color: t.textMuted, fontSize: 13 }}>
            No settings available for your role.
          </div>
        ) : (
          <Outlet />
        )}
      </main>
    </div>
  );
}
