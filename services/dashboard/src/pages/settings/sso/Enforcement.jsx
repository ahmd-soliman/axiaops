import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTheme } from '../../../theme/ThemeContext';
import { listSSOConnections, updateSSOConnection } from '../../../api/client';
import { Spinner } from '../../../components/primitives';

// Enforcement pane: per-connection radio (optional / preferred / required).
// Mirrors the `enforcement` field on the connection — the same field exposed
// in the Connections edit modal — but presented as a focused stance picker
// because flipping to `required` is the action with real lockout potential.
//
// Guard: when an admin moves a connection to `required`, we surface a
// client-side "I've tested SSO recently and a real user can log in" gate
// before letting the PATCH go through. The server-side 409 confirm guard
// (plan §5.3) is not wired yet; this is the user-facing equivalent until
// the API ships it.

const LEVELS = [
  {
    value: 'optional',
    title: 'Optional',
    desc: 'Users see both SSO and password login. Use during pilot rollouts.',
  },
  {
    value: 'preferred',
    title: 'Preferred',
    desc: 'SSO is offered first; password remains as a fallback. Use mid-rollout.',
  },
  {
    value: 'required',
    title: 'Required',
    desc: 'Only SSO logins are accepted for users on verified domains. Native passwords blocked. Use after verifying SSO works end-to-end.',
  },
];

export default function Enforcement() {
  const { isDark } = useTheme();
  const qc = useQueryClient();

  const conns = useQuery({ queryKey: ['sso-connections'], queryFn: listSSOConnections });

  const [pendingRequired, setPendingRequired] = useState(null); // { connection, prevValue }
  const [topError, setTopError] = useState('');
  const [savedTickFor, setSavedTickFor] = useState(null);
  const [pendingId, setPendingId] = useState(null);

  const invalidate = () => qc.invalidateQueries({ queryKey: ['sso-connections'] });

  const updateMutation = useMutation({
    mutationFn: ({ id, enforcement }) => updateSSOConnection(id, { enforcement }),
    onSuccess: (_data, vars) => {
      setTopError('');
      setSavedTickFor(vars.id);
      setTimeout(() => setSavedTickFor((cur) => (cur === vars.id ? null : cur)), 1800);
      setPendingId(null);
      invalidate();
    },
    onError: (err) => { setTopError(humanize(err, 'Failed to update enforcement')); setPendingId(null); },
  });

  function handleChange(connection, nextValue) {
    if (nextValue === connection.enforcement) return;
    if (nextValue === 'required') {
      setPendingRequired({ connection, nextValue });
      return;
    }
    setPendingId(connection.id);
    updateMutation.mutate({ id: connection.id, enforcement: nextValue });
  }

  return (
    <div>
      <p style={{ margin: 0, marginBottom: 16, fontSize: 13, color: 'var(--color-text-mid)' }}>
        Enforcement controls how SSO and native passwords coexist on a per-connection basis. Move from optional → preferred → required as your rollout matures.
      </p>

      {topError && (
        <Banner color={'var(--color-error)'} bg={isDark ? 'rgba(239,68,68,0.15)' : '#fee2e2'}>
          {topError}
        </Banner>
      )}

      {conns.isPending ? (
        <div style={{ padding: 32, textAlign: 'center' }}><Spinner /></div>
      ) : conns.isError ? (
        <div style={{ padding: 24, color: 'var(--color-error)' }}>Failed to load connections.</div>
      ) : (conns.data || []).length === 0 ? (
        <div style={{ padding: 32, textAlign: 'center', color: 'var(--color-text-muted)', fontSize: 13 }}>
          No connections yet. Create one in the Connections tab to set its enforcement stance.
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          {(conns.data || []).map((c) => (
            <ConnectionCard
              key={c.id}
              connection={c}
              isDark={isDark}
              disabled={pendingId === c.id}
              savedTick={savedTickFor === c.id}
              onChange={(next) => handleChange(c, next)}
            />
          ))}
        </div>
      )}

      {pendingRequired && (
        <RequiredGuardModal
          connection={pendingRequired.connection}
          onCancel={() => setPendingRequired(null)}
          onConfirm={() => {
            setPendingId(pendingRequired.connection.id);
            updateMutation.mutate({
              id: pendingRequired.connection.id,
              enforcement: 'required',
            });
            setPendingRequired(null);
          }}
          isDark={isDark}
        />
      )}
    </div>
  );
}

function ConnectionCard({ connection, isDark, disabled, savedTick, onChange }) {
  const border = isDark ? 'rgba(255,255,255,0.08)' : '#e5e7eb';
  const surface = isDark ? 'rgba(255,255,255,0.03)' : '#fff';
  const current = connection.enforcement || 'optional';
  return (
    <section
      style={{
        border: `1px solid ${border}`,
        borderRadius: 8,
        padding: 16,
        backgroundColor: surface,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
        <h3 style={{ margin: 0, fontSize: 14, fontWeight: 700, color: 'var(--color-text)' }}>
          {connection.label} <span style={{ fontSize: 11, color: 'var(--color-text-muted)', fontWeight: 500 }}>({connection.protocol}, {connection.status})</span>
        </h3>
        {savedTick && <span style={{ fontSize: 12, color: '#10b981' }}>Saved</span>}
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {LEVELS.map((lvl) => {
          const checked = current === lvl.value;
          return (
            <label
              key={lvl.value}
              style={{
                display: 'flex',
                gap: 10,
                padding: 10,
                border: `1px solid ${checked ? 'var(--color-accent)' : border}`,
                borderRadius: 6,
                backgroundColor: checked ? (isDark ? 'rgba(255,255,255,0.04)' : '#f9fafb') : 'transparent',
                cursor: disabled ? 'not-allowed' : 'pointer',
                opacity: disabled ? 0.6 : 1,
              }}
            >
              <input
                type="radio"
                name={`enf-${connection.id}`}
                value={lvl.value}
                checked={checked}
                onChange={() => onChange(lvl.value)}
                disabled={disabled}
                style={{ marginTop: 3 }}
              />
              <div>
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text)' }}>{lvl.title}</div>
                <div style={{ fontSize: 12, color: 'var(--color-text-mid)', marginTop: 2 }}>{lvl.desc}</div>
              </div>
            </label>
          );
        })}
      </div>
    </section>
  );
}

function RequiredGuardModal({ connection, onCancel, onConfirm, isDark }) {
  const [acknowledged, setAcknowledged] = useState(false);
  return (
    <div
      onClick={onCancel}
      style={{
        position: 'fixed', inset: 0, backgroundColor: 'rgba(0,0,0,0.5)',
        display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          width: 480, maxWidth: '90vw', backgroundColor: isDark ? '#1f2937' : '#fff',
          color: 'var(--color-text)', borderRadius: 8, padding: 20,
          boxShadow: '0 20px 50px rgba(0,0,0,0.3)',
        }}
      >
        <h2 style={{ margin: 0, marginBottom: 12, fontSize: 16, fontWeight: 700 }}>
          Require SSO for {connection.label}?
        </h2>
        <p style={{ margin: 0, marginBottom: 12, fontSize: 13, color: 'var(--color-text-mid)', lineHeight: '20px' }}>
          Once enforcement is <strong>required</strong>, users on this connection's domains can no longer sign in with passwords. If SSO is misconfigured, those users will be locked out and only an owner with a non-SSO email can rescue them.
        </p>
        <label style={{ display: 'flex', gap: 8, padding: 10, border: `1px solid var(--color-border)`, borderRadius: 6, marginBottom: 16, fontSize: 13, color: 'var(--color-text)', alignItems: 'flex-start' }}>
          <input
            type="checkbox"
            checked={acknowledged}
            onChange={(e) => setAcknowledged(e.target.checked)}
            style={{ marginTop: 3 }}
          />
          <span>I have tested SSO recently and verified that a real user on this connection can log in successfully.</span>
        </label>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <button type="button" onClick={onCancel} style={ghostButton()}>Cancel</button>
          <button
            type="button"
            disabled={!acknowledged}
            onClick={onConfirm}
            style={{
              ...primaryButton(),
              backgroundColor: 'var(--color-error)',
              opacity: acknowledged ? 1 : 0.5,
              cursor: acknowledged ? 'pointer' : 'not-allowed',
            }}
          >
            Require SSO
          </button>
        </div>
      </div>
    </div>
  );
}

function Banner({ children, color, bg }) {
  return (
    <div style={{ padding: '8px 12px', marginBottom: 12, borderRadius: 6, color, backgroundColor: bg, fontSize: 13 }}>
      {children}
    </div>
  );
}

function primaryButton() {
  return {
    padding: '7px 14px',
    border: 'none',
    borderRadius: 6,
    backgroundColor: 'var(--color-accent)',
    color: 'var(--color-text-on-dark)',
    fontWeight: 600,
    fontSize: 13,
    cursor: 'pointer',
  };
}

function ghostButton() {
  return {
    padding: '5px 10px',
    border: `1px solid var(--color-border)`,
    borderRadius: 6,
    backgroundColor: 'transparent',
    color: 'var(--color-text)',
    fontSize: 12,
    fontWeight: 600,
    cursor: 'pointer',
  };
}

function humanize(err, fallback) {
  if (!err) return fallback;
  if (err.status === 403) return 'You do not have permission to manage SSO.';
  if (err.status === 404) return 'Connection no longer exists.';
  if (err.status === 409) {
    // Reserved for the server-side "have you tested SSO recently?" 409
    // confirm guard once it ships (plan §5.3).
    return parseAPIError(err) || 'Re-confirm SSO works before requiring it.';
  }
  return err.message || fallback;
}

function parseAPIError(err) {
  if (!err?.body) return '';
  try {
    const parsed = JSON.parse(err.body);
    return parsed.error || parsed.message || '';
  } catch {
    return err.body;
  }
}
