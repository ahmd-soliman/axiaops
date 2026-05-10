import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTheme } from '../../../theme/ThemeContext';
import {
  createSSODomain,
  deleteSSODomain,
  listSSOConnections,
  listSSODomains,
  verifySSODomain,
} from '../../../api/client';
import { Spinner } from '../../../components/primitives';

// Domains pane: list verified / pending domains + add + verify + delete.
//
// Verification is a TXT-record flow. The server returns the TXT value only
// once — on the create response — and strips it from every subsequent list
// response (postgres/sso.go ListSSODomains). The Add modal therefore stays
// open after create, surfacing the record with a copy button until the
// admin acknowledges. Closing the modal without copying loses the token
// and forces a delete + re-add.

export default function Domains() {
  const { theme: t, isDark } = useTheme();
  const qc = useQueryClient();

  const [adding, setAdding] = useState(false);
  const [topError, setTopError] = useState('');
  const [verifyingId, setVerifyingId] = useState(null);
  const [deletingId, setDeletingId] = useState(null);

  const conns   = useQuery({ queryKey: ['sso-connections'], queryFn: listSSOConnections });
  const domains = useQuery({ queryKey: ['sso-domains'],     queryFn: listSSODomains });

  const connLabel = useMemo(() => {
    const map = new Map();
    (conns.data || []).forEach((c) => map.set(c.id, c.label));
    return (id) => map.get(id) || id;
  }, [conns.data]);

  const invalidate = () => qc.invalidateQueries({ queryKey: ['sso-domains'] });

  const verifyMutation = useMutation({
    mutationFn: ({ id }) => verifySSODomain(id),
    onSuccess: (resp, vars) => {
      invalidate();
      const safeDomain = sanitizeForDialog(vars.domain);
      if (resp && resp.verified === false) {
        setTopError(`${safeDomain}: TXT record not found yet — DNS often takes a few minutes to propagate. Reason: ${resp.reason || 'unknown'}.`);
      } else {
        setTopError('');
      }
      setVerifyingId(null);
    },
    onError: (err, vars) => {
      const safeDomain = sanitizeForDialog(vars.domain);
      setTopError(`${safeDomain}: ${humanize(err, 'Verification failed')}`);
      setVerifyingId(null);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: ({ id }) => deleteSSODomain(id),
    onSuccess: () => { invalidate(); setDeletingId(null); },
    onError: (err) => { setTopError(humanize(err, 'Failed to delete domain')); setDeletingId(null); },
  });

  const noConnections = !conns.isPending && (conns.data || []).length === 0;
  const addDisabled   = conns.isPending || noConnections;
  const addTitle      = conns.isPending ? 'Loading connections…' : noConnections ? 'Create a connection first.' : undefined;

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <p style={{ margin: 0, fontSize: 13, color: t.textMid }}>
          Verified domains route logins to a connection. Add a domain, publish the TXT record we hand you, then click Verify.
        </p>
        <button
          type="button"
          onClick={() => setAdding(true)}
          disabled={addDisabled}
          title={addTitle}
          style={{ ...primaryButton(t), opacity: addDisabled ? 0.5 : 1, cursor: addDisabled ? 'not-allowed' : 'pointer' }}
        >
          Add domain
        </button>
      </div>

      {topError && (
        <Banner color={isDark ? '#fca5a5' : '#b91c1c'} bg={isDark ? 'rgba(239,68,68,0.15)' : '#fee2e2'}>
          {topError}
        </Banner>
      )}

      {domains.isPending ? (
        <div style={{ padding: 32, textAlign: 'center' }}><Spinner /></div>
      ) : domains.isError ? (
        <div style={{ padding: 24, color: t.error }}>Failed to load domains.</div>
      ) : (domains.data || []).length === 0 ? (
        <EmptyState t={t} />
      ) : (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
          <thead>
            <tr style={{ borderBottom: `1px solid ${t.border}` }}>
              <Th t={t}>Domain</Th>
              <Th t={t}>Connection</Th>
              <Th t={t}>Status</Th>
              <Th t={t}>Verified</Th>
              <Th t={t}>Expires</Th>
              <Th t={t}></Th>
            </tr>
          </thead>
          <tbody>
            {(domains.data || []).map((d) => (
              <tr key={d.id} style={{ borderBottom: `1px solid ${t.border}` }}>
                <Td t={t}><code style={{ fontSize: 13 }}>{d.domain}</code></Td>
                <Td t={t}>{connLabel(d.sso_connection_id)}</Td>
                <Td t={t}><StatusBadge status={d.status} t={t} /></Td>
                <Td t={t}>{formatDate(d.verified_at)}</Td>
                <Td t={t}>{formatDate(d.expires_at)}</Td>
                <Td t={t}>
                  <button
                    type="button"
                    onClick={() => {
                      setVerifyingId(d.id);
                      verifyMutation.mutate({ id: d.id, domain: d.domain });
                    }}
                    disabled={verifyingId === d.id}
                    style={ghostButton(t)}
                  >
                    {verifyingId === d.id ? 'Verifying…' : d.status === 'verified' ? 'Re-verify' : 'Verify'}
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      const safeDomain = sanitizeForDialog(d.domain);
                      const safeConn   = sanitizeForDialog(connLabel(d.sso_connection_id));
                      if (confirm(`Delete domain ${safeDomain}? Users on this domain will stop being routed to ${safeConn}.`)) {
                        setDeletingId(d.id);
                        deleteMutation.mutate({ id: d.id });
                      }
                    }}
                    disabled={deletingId === d.id}
                    style={{ ...ghostButton(t), color: t.error, marginLeft: 6 }}
                  >
                    {deletingId === d.id ? 'Deleting…' : 'Delete'}
                  </button>
                </Td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {adding && (
        <AddDomainModal
          connections={conns.data || []}
          onClose={() => setAdding(false)}
          onCreated={invalidate}
          t={t}
          isDark={isDark}
        />
      )}
    </div>
  );
}

function AddDomainModal({ connections, onClose, onCreated, t, isDark }) {
  const [connectionId, setConnectionId] = useState(connections[0]?.id || '');
  const [domain, setDomain] = useState('');
  const [created, setCreated] = useState(null); // server response with verification_token
  const [error, setError] = useState('');

  const createMutation = useMutation({
    mutationFn: () => createSSODomain({ ssoConnectionId: connectionId, domain: domain.trim() }),
    onSuccess: (row) => {
      setCreated(row);
      onCreated();
    },
    onError: (err) => setError(humanize(err, 'Create failed')),
  });

  if (created) {
    return (
      <ModalShell title="Domain added — publish this TXT record" onClose={onClose} t={t} isDark={isDark}>
        <p style={{ margin: 0, marginBottom: 12, fontSize: 13, color: t.textMid }}>
          Add this TXT record at the apex of <strong>{created.domain}</strong>. Then come back and click <strong>Verify</strong>.
        </p>
        <RecordDisplay label="Host"  value="@" t={t} isDark={isDark} />
        <RecordDisplay label="Type"  value="TXT" t={t} isDark={isDark} />
        <RecordDisplay
          label="Value"
          value={`axiaops-domain-verification=${created.verification_token}`}
          t={t}
          isDark={isDark}
        />
        <Banner color={isDark ? '#fbbf24' : '#92400e'} bg={isDark ? 'rgba(251,191,36,0.12)' : '#fef3c7'}>
          This token is only shown <strong>once</strong>. Copy it before closing — once dismissed, you'll have to delete and re-add the domain to see it again.
        </Banner>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 12 }}>
          <button type="button" onClick={onClose} style={primaryButton(t)}>Done</button>
        </div>
      </ModalShell>
    );
  }

  const submitDisabled = createMutation.isPending || !connectionId || !domain.trim();

  return (
    <ModalShell
      title="Add domain"
      onClose={onClose}
      lockClose={createMutation.isPending}
      t={t}
      isDark={isDark}
    >
      <form
        onSubmit={(e) => { e.preventDefault(); if (!submitDisabled) createMutation.mutate(); }}
        style={{ display: 'flex', flexDirection: 'column', gap: 12 }}
      >
        <Field label="Connection" hint="Logins from this domain redirect to this IdP.">
          <select value={connectionId} onChange={(e) => setConnectionId(e.target.value)} style={inputStyle(t)}>
            {connections.map((c) => (
              <option key={c.id} value={c.id}>{c.label} ({c.protocol})</option>
            ))}
          </select>
        </Field>
        <Field label="Domain" hint="Apex domain only — e.g. acme.com (not www.acme.com).">
          <input
            type="text"
            value={domain}
            onChange={(e) => setDomain(e.target.value)}
            placeholder="acme.com"
            required
            autoFocus
            style={inputStyle(t)}
          />
        </Field>
        {error && <Banner color={t.error} bg={isDark ? 'rgba(239,68,68,0.15)' : '#fee2e2'}>{error}</Banner>}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 8 }}>
          <button type="button" onClick={onClose} style={ghostButton(t)}>Cancel</button>
          <button
            type="submit"
            disabled={submitDisabled}
            style={{ ...primaryButton(t), opacity: submitDisabled ? 0.5 : 1, cursor: submitDisabled ? 'not-allowed' : 'pointer' }}
          >
            {createMutation.isPending ? 'Creating…' : 'Create + show TXT record'}
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

function RecordDisplay({ label, value, t, isDark }) {
  const [copied, setCopied] = useState(false);
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
      <span style={{ width: 60, fontSize: 11, color: t.textMuted, fontWeight: 600 }}>{label}</span>
      <code
        style={{
          flex: 1,
          padding: '6px 10px',
          backgroundColor: isDark ? 'rgba(255,255,255,0.05)' : '#f3f4f6',
          borderRadius: 4,
          fontSize: 12,
          color: t.text,
          overflowX: 'auto',
          whiteSpace: 'nowrap',
        }}
      >
        {value}
      </code>
      <button
        type="button"
        onClick={async () => {
          try {
            await navigator.clipboard.writeText(value);
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          } catch {
            /* clipboard blocked — leave button label unchanged */
          }
        }}
        style={ghostButton(t)}
      >
        {copied ? 'Copied' : 'Copy'}
      </button>
    </div>
  );
}

function ModalShell({ children, onClose, lockClose, t, isDark, title }) {
  // Backdrop dismissal is blocked while lockClose is true — keeps the
  // create-then-show-TXT-token flow stable through the in-flight mutation
  // and prevents setError on an unmounted modal swallowing failures.
  const handleBackdropClick = lockClose ? undefined : onClose;
  return (
    <div
      onClick={handleBackdropClick}
      style={{
        position: 'fixed', inset: 0, backgroundColor: 'rgba(0,0,0,0.5)',
        display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          width: 560, maxWidth: '90vw', maxHeight: '90vh', overflowY: 'auto',
          backgroundColor: isDark ? '#1f2937' : '#fff',
          color: t.text,
          borderRadius: 8, padding: 20,
          boxShadow: '0 20px 50px rgba(0,0,0,0.3)',
        }}
      >
        <h2 style={{ margin: 0, marginBottom: 16, fontSize: 16, fontWeight: 700 }}>{title}</h2>
        {children}
      </div>
    </div>
  );
}

function Field({ label, hint, children }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12 }}>
      <span style={{ fontWeight: 600 }}>{label}</span>
      {children}
      {hint && <span style={{ fontSize: 11, opacity: 0.7 }}>{hint}</span>}
    </label>
  );
}

function EmptyState({ t }) {
  return (
    <div style={{ padding: 32, textAlign: 'center', color: t.textMuted, fontSize: 13 }}>
      No domains verified yet. Add one to start routing logins from your corporate email domain.
    </div>
  );
}

function StatusBadge({ status, t }) {
  // Inline colored label — no pill chrome. Color carries the state cue.
  const fg = {
    verified: '#10b981',
    pending:  '#f59e0b',
    stale:    '#ef4444',
    revoked:  t.textMuted,
  }[status] || t.textMuted;
  return (
    <span style={{ fontSize: 11, fontWeight: 600, color: fg, letterSpacing: 0.2 }}>
      {status}
    </span>
  );
}

function Th({ t, children }) {
  return (
    <th style={{ padding: '10px 12px', textAlign: 'left', fontWeight: 600, fontSize: 12, color: t.textMuted, letterSpacing: 0.3 }}>
      {children}
    </th>
  );
}

function Td({ t, children }) {
  return <td style={{ padding: '10px 12px', color: t.text }}>{children}</td>;
}

function Banner({ children, color, bg }) {
  return (
    <div style={{ padding: '8px 12px', marginTop: 8, borderRadius: 6, color, backgroundColor: bg, fontSize: 13 }}>
      {children}
    </div>
  );
}

function inputStyle(t) {
  return {
    padding: '6px 10px',
    border: `1px solid ${t.border}`,
    borderRadius: 6,
    fontSize: 13,
    backgroundColor: t.bg,
    color: t.text,
    width: '100%',
    boxSizing: 'border-box',
  };
}

function primaryButton(t) {
  return {
    padding: '7px 14px',
    border: 'none',
    borderRadius: 6,
    backgroundColor: t.accent,
    color: t.textOnDark,
    fontWeight: 600,
    fontSize: 13,
    cursor: 'pointer',
  };
}

function ghostButton(t) {
  return {
    padding: '5px 10px',
    border: `1px solid ${t.border}`,
    borderRadius: 6,
    backgroundColor: 'transparent',
    color: t.text,
    fontSize: 12,
    fontWeight: 600,
    cursor: 'pointer',
  };
}

function formatDate(s) {
  if (!s) return '—';
  try {
    return new Date(s).toLocaleDateString();
  } catch {
    return s;
  }
}

function humanize(err, fallback) {
  if (!err) return fallback;
  const detail = parseAPIError(err);
  if (err.status === 400) return detail || 'Invalid domain.';
  if (err.status === 403) return 'You do not have permission to manage SSO domains.';
  if (err.status === 404) return 'Domain no longer exists.';
  if (err.status === 502) return 'DNS lookup failed — try again in a minute.';
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

// See Connections.jsx for rationale; same C0/C1/bidi/separator strip.
function sanitizeForDialog(s) {
  let out = '';
  for (const ch of String(s ?? '')) {
    const cp = ch.codePointAt(0);
    const drop =
      cp < 0x20 ||
      cp === 0x7f ||
      (cp >= 0x80 && cp <= 0x9f) ||
      cp === 0x200e || cp === 0x200f ||
      (cp >= 0x202a && cp <= 0x202e) ||
      cp === 0x2028 || cp === 0x2029;
    if (drop) continue;
    out += ch;
    if (out.length >= 80) break;
  }
  return out;
}
