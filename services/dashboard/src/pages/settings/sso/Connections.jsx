import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTheme } from '../../../theme/ThemeContext';
import {
  createSSOConnection,
  deleteSSOConnection,
  listSSOConnections,
  updateSSOConnection,
} from '../../../api/client';
import { Spinner } from '../../../components/primitives';

// Connections pane: list of OIDC connections + add/edit/delete.
//
// Wizard is intentionally minimal for slice 5 — protocol is OIDC-only here
// (SAML lands in Phase C, the API already accepts saml_* fields), label,
// discovery URL, client ID, client secret, optional tenant ID, default role.
// The server forces status=draft on create per handler.go; activation is a
// separate edit.
//
// All fields below the protocol picker are OIDC-shaped. The container is
// already gated on PERM.SSO_MANAGE so this whole file assumes owner.

const DEFAULT_ROLES = ['viewer', 'member', 'admin'];
const STATUSES      = ['draft', 'active', 'disabled'];
const ENFORCEMENTS  = ['optional', 'preferred', 'required'];

export default function Connections() {
  const { theme: t, isDark } = useTheme();
  const qc = useQueryClient();

  const [adding, setAdding]     = useState(false);
  const [editing, setEditing]   = useState(null); // connection object or null
  const [topError, setTopError] = useState('');
  const [deletingId, setDeletingId] = useState(null);

  const conns = useQuery({ queryKey: ['sso-connections'], queryFn: listSSOConnections });

  const invalidate = () => qc.invalidateQueries({ queryKey: ['sso-connections'] });

  const deleteMutation = useMutation({
    mutationFn: ({ id }) => deleteSSOConnection(id),
    onSuccess: () => { invalidate(); setDeletingId(null); },
    onError: (err) => { setTopError(humanize(err, 'Failed to delete connection')); setDeletingId(null); },
  });

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <p style={{ margin: 0, fontSize: 13, color: t.textMid }}>
          OIDC connections. Each one binds an IdP (e.g. Entra, Okta, Google) to your org.
        </p>
        <button type="button" onClick={() => setAdding(true)} style={primaryButton(t)}>
          Add connection
        </button>
      </div>

      {topError && (
        <Banner color={isDark ? '#fca5a5' : '#b91c1c'} bg={isDark ? 'rgba(239,68,68,0.15)' : '#fee2e2'}>
          {topError}
        </Banner>
      )}

      {conns.isPending ? (
        <div style={{ padding: 32, textAlign: 'center' }}><Spinner /></div>
      ) : conns.isError ? (
        <div style={{ padding: 24, color: t.error }}>Failed to load connections.</div>
      ) : (conns.data || []).length === 0 ? (
        <EmptyState t={t} />
      ) : (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
          <thead>
            <tr style={{ borderBottom: `1px solid ${t.border}` }}>
              <Th t={t}>Label</Th>
              <Th t={t}>Protocol</Th>
              <Th t={t}>Status</Th>
              <Th t={t}>Enforcement</Th>
              <Th t={t}>Default role</Th>
              <Th t={t}></Th>
            </tr>
          </thead>
          <tbody>
            {(conns.data || []).map((c) => (
              <tr key={c.id} style={{ borderBottom: `1px solid ${t.border}` }}>
                <Td t={t}>{c.label || '—'}</Td>
                <Td t={t}><code style={{ fontSize: 12 }}>{c.protocol}</code></Td>
                <Td t={t}><StatusBadge status={c.status} t={t} /></Td>
                <Td t={t}>{c.enforcement || 'optional'}</Td>
                <Td t={t}>{c.default_role || 'viewer'}</Td>
                <Td t={t}>
                  <button type="button" onClick={() => setEditing(c)} style={ghostButton(t)}>Edit</button>
                  <button
                    type="button"
                    onClick={() => {
                      const safeLabel = sanitizeForDialog(c.label);
                      if (confirm(`Delete connection "${safeLabel}"? Users redirecting through this connection will fail to log in.`)) {
                        setDeletingId(c.id);
                        deleteMutation.mutate({ id: c.id });
                      }
                    }}
                    disabled={deletingId === c.id}
                    style={{ ...ghostButton(t), color: t.error, marginLeft: 6 }}
                  >
                    {deletingId === c.id ? 'Deleting…' : 'Delete'}
                  </button>
                </Td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {adding && (
        <ConnectionModal
          mode="create"
          onClose={() => setAdding(false)}
          onSaved={() => { setAdding(false); invalidate(); }}
          t={t}
          isDark={isDark}
        />
      )}
      {editing && (
        <ConnectionModal
          mode="edit"
          existing={editing}
          onClose={() => setEditing(null)}
          onSaved={() => { setEditing(null); invalidate(); }}
          t={t}
          isDark={isDark}
        />
      )}
    </div>
  );
}

function ConnectionModal({ mode, existing, onClose, onSaved, t, isDark }) {
  const isEdit = mode === 'edit';
  const [form, setForm] = useState(() => ({
    protocol:           existing?.protocol           || 'oidc',
    label:              existing?.label              || '',
    status:             existing?.status             || 'draft',
    enforcement:        existing?.enforcement        || 'optional',
    default_role:       existing?.default_role       || 'viewer',
    // ForceReauth is on by default for new connections — secure posture.
    // Existing rows reflect whatever the API returned (which respects the
    // DB default if the column was unset pre-migration-023).
    force_reauth:       existing?.force_reauth ?? true,
    oidc_discovery_url: existing?.oidc_discovery_url || '',
    oidc_client_id:     existing?.oidc_client_id     || '',
    oidc_tenant_id:     existing?.oidc_tenant_id     || '',
    oidc_client_secret: '', // never returned; only sent if non-empty
  }));
  const [error, setError] = useState('');

  const set = (k) => (e) => setForm((f) => ({ ...f, [k]: e.target.value }));

  const saveMutation = useMutation({
    mutationFn: () => {
      // Strip blank optional fields so PATCH treats absent as "no change"
      // and POST doesn't send unnecessary keys. oidc_client_secret is the
      // only field where empty string genuinely means "leave it alone."
      const payload = {
        protocol: form.protocol,
        label: form.label.trim(),
        enforcement: form.enforcement,
        default_role: form.default_role,
        // Always send force_reauth so the *bool API contract sees a real
        // value (true/false) rather than nil = "no change". On PATCH this
        // overwrites the column even when the toggle hasn't been touched,
        // which is fine — the form initialised from the existing value.
        force_reauth: !!form.force_reauth,
        oidc_discovery_url: form.oidc_discovery_url.trim(),
        oidc_client_id: form.oidc_client_id.trim(),
        oidc_tenant_id: form.oidc_tenant_id.trim(),
      };
      if (form.oidc_client_secret) payload.oidc_client_secret = form.oidc_client_secret;
      if (isEdit) payload.status = form.status; // create forces draft server-side
      return isEdit
        ? updateSSOConnection(existing.id, payload)
        : createSSOConnection(payload);
    },
    onSuccess: onSaved,
    onError: (err) => setError(humanize(err, 'Save failed')),
  });

  // OIDC fields are required on create — on edit, an absent field means
  // "no change", so empty-on-edit is allowed and the server just keeps the
  // existing ciphertext (client_secret) or value.
  const submitDisabled =
    saveMutation.isPending ||
    !form.label.trim() ||
    (!isEdit && (!form.oidc_discovery_url.trim() || !form.oidc_client_id.trim() || !form.oidc_client_secret));

  return (
    <ModalShell
      onClose={onClose}
      lockClose={saveMutation.isPending}
      t={t}
      isDark={isDark}
      title={isEdit ? `Edit ${existing.label}` : 'Add SSO connection'}
    >
      <form
        onSubmit={(e) => { e.preventDefault(); if (!submitDisabled) saveMutation.mutate(); }}
        style={{ display: 'flex', flexDirection: 'column', gap: 12 }}
      >
        <Field label="Protocol" hint="SAML lands in Phase C — only OIDC is wired up today.">
          <select value={form.protocol} onChange={set('protocol')} style={inputStyle(t)} disabled={isEdit}>
            <option value="oidc">OIDC</option>
            <option value="saml" disabled>SAML (Phase C)</option>
          </select>
        </Field>

        <Field label="Label" hint="Shown to admins in this list and in audit logs.">
          <input type="text" value={form.label} onChange={set('label')} required maxLength={120} style={inputStyle(t)} />
        </Field>

        {isEdit && (
          <Field label="Status">
            <select value={form.status} onChange={set('status')} style={inputStyle(t)}>
              {STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
            </select>
          </Field>
        )}

        <Field label="Enforcement" hint="optional = users can choose; preferred = SSO offered first; required = native passwords blocked.">
          <select value={form.enforcement} onChange={set('enforcement')} style={inputStyle(t)}>
            {ENFORCEMENTS.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
        </Field>

        <Field label="Default role" hint="Role assigned to JIT-provisioned users when no group mapping matches.">
          <select value={form.default_role} onChange={set('default_role')} style={inputStyle(t)}>
            {DEFAULT_ROLES.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
        </Field>

        <Field
          label="Force re-authentication"
          hint="When on, every login forces the IdP to re-prompt for credentials regardless of an existing IdP session. Closes silent-identity-substitution on shared browsers. Turn OFF only if your IdP enforces its own session policy (e.g. Azure AD conditional access) and rejects prompt=login."
        >
          <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <input
              type="checkbox"
              checked={!!form.force_reauth}
              onChange={(e) => setForm((f) => ({ ...f, force_reauth: e.target.checked }))}
            />
            <span style={{ fontSize: 13, color: t.text }}>
              Send <code style={{ fontFamily: '"Geist Mono Variable", ui-monospace, monospace' }}>prompt=login</code> on the OIDC authorize URL
            </span>
          </label>
        </Field>

        <Divider t={t} label="OIDC settings" />

        <Field label="Discovery URL" hint="https://login.microsoftonline.com/{tenant}/v2.0/.well-known/openid-configuration">
          <input
            type="url"
            value={form.oidc_discovery_url}
            onChange={set('oidc_discovery_url')}
            required={!isEdit}
            placeholder="https://…/.well-known/openid-configuration"
            style={inputStyle(t)}
          />
        </Field>

        <Field label="Client ID">
          <input type="text" value={form.oidc_client_id} onChange={set('oidc_client_id')} required={!isEdit} style={inputStyle(t)} />
        </Field>

        <Field
          label="Client secret"
          hint={isEdit ? 'Leave blank to keep the current secret. Anything entered here replaces it.' : 'Will be encrypted at rest. Never returned in responses.'}
        >
          <input
            type="password"
            value={form.oidc_client_secret}
            onChange={set('oidc_client_secret')}
            required={!isEdit}
            autoComplete="new-password"
            style={inputStyle(t)}
          />
        </Field>

        <Field label="Tenant ID" hint="Entra/Azure AD only. Leave blank for generic OIDC.">
          <input type="text" value={form.oidc_tenant_id} onChange={set('oidc_tenant_id')} style={inputStyle(t)} />
        </Field>

        {error && <Banner color={t.error} bg={isDark ? 'rgba(239,68,68,0.15)' : '#fee2e2'}>{error}</Banner>}

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 8 }}>
          <button type="button" onClick={onClose} style={ghostButton(t)}>Cancel</button>
          <button type="submit" disabled={submitDisabled} style={{ ...primaryButton(t), opacity: submitDisabled ? 0.5 : 1, cursor: submitDisabled ? 'not-allowed' : 'pointer' }}>
            {saveMutation.isPending ? 'Saving…' : isEdit ? 'Save changes' : 'Save as draft'}
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

function ModalShell({ children, onClose, lockClose, t, isDark, title }) {
  // Backdrop click dismisses by default. While `lockClose` is true (e.g.
  // a save mutation is in flight) the click is swallowed — otherwise the
  // user can dismiss mid-save and the mutation's onError fires setError
  // on an unmounted component, swallowing the failure silently.
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
          width: 520, maxWidth: '90vw', maxHeight: '90vh', overflowY: 'auto',
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

function Divider({ t, label }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4 }}>
      <span style={{ flex: 1, height: 1, backgroundColor: t.border }} />
      <span style={{ fontSize: 11, color: t.textMuted, fontWeight: 600, letterSpacing: 0.4, textTransform: 'uppercase' }}>{label}</span>
      <span style={{ flex: 1, height: 1, backgroundColor: t.border }} />
    </div>
  );
}

function EmptyState({ t }) {
  return (
    <div style={{ padding: 32, textAlign: 'center', color: t.textMuted, fontSize: 13 }}>
      No SSO connections yet. Click <strong>Add connection</strong> to wire up your IdP.
    </div>
  );
}

function StatusBadge({ status, t }) {
  // Inline colored label — no pill chrome. Color carries the state cue.
  const fg = {
    active:   '#10b981',
    draft:    t.textMuted,
    disabled: '#ef4444',
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
    <div style={{ padding: '8px 12px', marginBottom: 0, borderRadius: 6, color, backgroundColor: bg, fontSize: 13 }}>
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

function humanize(err, fallback) {
  if (!err) return fallback;
  const detail = parseAPIError(err);
  if (err.status === 400) return detail || 'Invalid request — check the OIDC fields.';
  if (err.status === 403) return 'You do not have permission to manage SSO.';
  if (err.status === 404) return 'Connection no longer exists.';
  if (err.status === 409) return detail || 'Conflict.';
  return err.message || fallback;
}

// SSO handlers respond with `{"error":"..."}` JSON bodies (writeError in
// services/api/internal/sso/handler.go). Display the inner string, not the
// raw JSON envelope. Falls back to the raw body on parse failure so we
// never silently drop information.
function parseAPIError(err) {
  if (!err?.body) return '';
  try {
    const parsed = JSON.parse(err.body);
    return parsed.error || parsed.message || '';
  } catch {
    return err.body;
  }
}

// confirm() with user-controlled content is safe from DOM-injection but a
// label containing newlines or RTL-override characters can split the dialog
// into visually misleading sentences. Strip C0 + DEL + C1 controls, LRM/RLM
// bidi marks, LRE/RLE/PDF/LRO/RLO bidi-overrides, and line/paragraph
// separators, then cap to 80 chars before interpolation.
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
