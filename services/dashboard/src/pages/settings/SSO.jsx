import { useState } from 'react';
import { useTheme } from '../../theme/ThemeContext';
import Connections from './sso/Connections';
import Domains from './sso/Domains';
import Enforcement from './sso/Enforcement';
import GroupMappings from './sso/GroupMappings';

// SSO settings hub: horizontal tab strip + active pane.
// Per plan §5.3, this is owner-only — the route is gated on PERM.SSO_MANAGE
// in Settings.jsx, so non-owners never see the tab. The four sub-panes land
// in follow-up commits within slice 5; scaffold here is the container shell
// so routing and permission gating are wired in isolation.
const PANES = [
  { id: 'connections',    label: 'Connections' },
  { id: 'domains',        label: 'Domains' },
  { id: 'group-mappings', label: 'Group Mappings' },
  { id: 'enforcement',    label: 'Enforcement' },
];

export default function SSO() {
  const { theme: t, isDark } = useTheme();
  const [active, setActive] = useState(PANES[0].id);

  const tabBorder = isDark ? 'rgba(255,255,255,0.08)' : '#e5e7eb';

  return (
    <div style={{ padding: 24, color: t.textMid, maxWidth: 960 }}>
      <h1 style={{ margin: 0, color: t.text, fontSize: 22, fontWeight: 700 }}>Single Sign-On</h1>
      <p style={{ marginTop: 4, marginBottom: 20, color: t.textMuted, fontSize: 13 }}>
        Configure OIDC connections, verify domains, map groups to roles, and set enforcement.
      </p>
      <div role="tablist" style={{ display: 'flex', gap: 4, borderBottom: `1px solid ${tabBorder}`, marginBottom: 20 }}>
        {PANES.map((p) => {
          const isActive = active === p.id;
          return (
            <button
              key={p.id}
              type="button"
              role="tab"
              id={`sso-tab-${p.id}`}
              aria-controls={`sso-pane-${p.id}`}
              aria-selected={isActive}
              onClick={() => setActive(p.id)}
              style={{
                padding: '8px 14px',
                border: 'none',
                borderBottom: `2px solid ${isActive ? t.accent : 'transparent'}`,
                backgroundColor: 'transparent',
                color: isActive ? t.accent : t.textMid,
                fontSize: 13,
                fontWeight: isActive ? 700 : 500,
                cursor: 'pointer',
                marginBottom: -1,
              }}
            >
              {p.label}
            </button>
          );
        })}
      </div>
      <div role="tabpanel" id={`sso-pane-${active}`} aria-labelledby={`sso-tab-${active}`}>
        <Pane id={active} />
      </div>
    </div>
  );
}

function Pane({ id }) {
  if (id === 'connections')    return <Connections />;
  if (id === 'domains')        return <Domains />;
  if (id === 'group-mappings') return <GroupMappings />;
  if (id === 'enforcement')    return <Enforcement />;
  return null;
}
