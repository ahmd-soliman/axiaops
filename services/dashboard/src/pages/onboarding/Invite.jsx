import { useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTheme } from '../../theme/ThemeContext';
import { useToast } from '../../context/ToastContext';
import { createInvitation } from '../../api/client';

const ROLES = [
  { value: 'viewer', label: 'Viewer — read-only' },
  { value: 'member', label: 'Member — manage accounts and zombies' },
  { value: 'admin', label: 'Admin — full control except ownership' },
];

// resolveRedemptionURL mirrors the same-origin validator used by
// Settings → Members. The API emits a relative path when PUBLIC_HOST is
// unset (typical for dev / self-hosted-with-no-public-origin); resolve
// it against window.location.origin so the OOB-shared link is always a
// usable absolute URL. Protocol-relative ("//evil/...") and cross-origin
// absolutes are dropped — defence in depth against open-redirect-style
// abuse of the invitation flow. See pages/settings/Members.jsx for the
// canonical version of this validator and the rationale behind each
// branch.
function resolveRedemptionURL(raw) {
  if (typeof raw !== 'string' || raw === '') return null;
  if (raw.startsWith('//')) return null;
  if (raw.startsWith('/')) return window.location.origin + raw;
  try {
    const parsed = new URL(raw);
    if (parsed.origin === window.location.origin) return parsed.toString();
  } catch {
    /* malformed URL — drop */
  }
  return null;
}

// Step 2 of 3 — invite teammates. Skippable. Each row is sent as a separate
// POST /v1/invitations; failures are toasted but don't block advancement.
// See docs/onboarding-wizard.md §8.3.
//
// AxiaOps has no SMTP — the redemption URL returned by POST /v1/invitations
// is the ONLY way the invitee can be reached. After the batch finishes the
// wizard surfaces every minted URL in a copy-friendly list before letting
// the user advance, otherwise the tokens are stranded server-side until
// expiry and the invitees never hear anything.
export default function OnboardingInvite() {
  const { isDark } = useTheme();
  const navigate = useNavigate();
  const { toast } = useToast();

  // Monotonic id generator for row keys. Array index as key would cause
  // controlled-input state (focus, IME composition, validation messages)
  // to leak between sibling rows when one is removed mid-list — React
  // reconciles by key, and on removal the surviving rows' keys shift up.
  // A per-row stable id keeps React's reconciler aligned with the user's
  // mental model.
  const nextRowId = useRef(0);
  const makeRow = (patch = {}) => ({ id: nextRowId.current++, email: '', role: 'member', ...patch });
  const [rows, setRows] = useState(() => [makeRow()]);
  const [sending, setSending] = useState(false);
  // Successful invites from the most recent batch — each entry is
  // {email, role, url}. Populated by sendAll() once the loop completes.
  // While this list is non-empty the wizard shows a copy-link panel
  // instead of advancing automatically.
  const [createdInvites, setCreatedInvites] = useState([]);
  // The url whose copy button was just pressed — drives the per-row "✓"
  // affordance for ~2s. Indexed by URL because emails aren't guaranteed
  // unique within a batch (admins occasionally re-type) and the URL is.
  const [copiedURL, setCopiedURL] = useState('');

  const border = isDark ? 'rgba(255,255,255,0.12)' : '#e5e7eb';
  const inputBg = isDark ? 'rgba(0,0,0,0.2)' : '#fff';

  function updateRow(id, patch) {
    setRows((prev) => prev.map((r) => (r.id === id ? { ...r, ...patch } : r)));
  }
  function addRow() {
    setRows((prev) => [...prev, makeRow()]);
  }
  function removeRow(id) {
    setRows((prev) => prev.filter((r) => r.id !== id));
  }

  async function copyURL(url) {
    try {
      await navigator?.clipboard?.writeText(url);
      setCopiedURL(url);
      setTimeout(() => setCopiedURL((v) => (v === url ? '' : v)), 2000);
    } catch {
      // Clipboard API unavailable (insecure context, etc.).
      // The input is auto-selected on focus so Cmd/Ctrl-C still works.
    }
  }

  async function sendAll() {
    const valid = rows.filter((r) => r.email.trim() !== '');
    if (valid.length === 0) {
      navigate('/onboarding/aws-account', { replace: true });
      return;
    }
    setSending(true);
    const created = [];
    for (const row of valid) {
      const email = row.email.trim();
      try {
        const resp = await createInvitation(email, row.role);
        const url = resolveRedemptionURL(resp?.redemption_url);
        if (url) {
          created.push({ email, role: row.role, url });
        } else {
          // Server minted the invitation (pending_memberships row is in
          // the DB) but the URL we got back is unusable — either missing,
          // protocol-relative, or absolute to a different origin. Most
          // common cause: PUBLIC_HOST on the API is set to a host the
          // admin can't reach. The token is unrecoverable from this UI
          // (server only stores the hash); surface the situation so the
          // admin doesn't silently lose the invitee.
          toast(
            `${email} was invited but the link could not be resolved. Revoke the pending invitation from Settings → Members and check the server's PUBLIC_HOST setting.`,
            'error',
          );
        }
      } catch (err) {
        const code = err?.body?.error;
        const msg = err?.body?.message || `Could not invite ${email}.`;
        if (code === 'already_a_member') {
          toast(`${email} is already a member.`, 'info');
        } else if (code === 'user_exists_use_memberships') {
          toast(`${email} already has an account — add them from Settings → Members.`, 'info');
        } else {
          toast(msg, 'error');
        }
      }
    }
    setSending(false);
    if (created.length === 0) {
      // Every row either failed or returned no usable URL — nothing to
      // show, just advance. Errors have already been toasted.
      navigate('/onboarding/aws-account', { replace: true });
      return;
    }
    setCreatedInvites(created);
    toast(
      `Invitation${created.length === 1 ? '' : 's'} created — copy the link${created.length === 1 ? '' : 's'} below to share.`,
      'success',
    );
  }

  function skip() {
    navigate('/onboarding/aws-account', { replace: true });
  }

  function continueToNext() {
    navigate('/onboarding/aws-account', { replace: true });
  }

  if (createdInvites.length > 0) {
    return (
      <div>
        <h1 style={{ color: 'var(--color-text)', fontSize: 26, fontWeight: 700, margin: 0, marginBottom: 8 }}>
          Share these invitation links
        </h1>
        <p style={{ color: 'var(--color-text-mid)', fontSize: 14, marginTop: 0, marginBottom: 24 }}>
          AxiaOps doesn't send invitation emails. Copy each link below and
          share it with the invitee over Slack, email, or another private
          channel. Anyone with a link can redeem it, so don't post them
          publicly. You can revoke a pending invitation any time from
          Settings → Members.
        </p>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginBottom: 24 }}>
          {createdInvites.map((inv) => (
            <div
              key={inv.url}
              style={{
                padding: 12,
                borderRadius: 6,
                backgroundColor: isDark ? 'rgba(34,197,94,0.10)' : '#ecfdf5',
                border: `1px solid ${isDark ? 'rgba(34,197,94,0.35)' : '#86efac'}`,
              }}
            >
              <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text)', marginBottom: 8 }}>
                <strong>{inv.email}</strong> ({inv.role})
              </div>
              <div style={{ position: 'relative' }}>
                <input
                  type="text"
                  readOnly
                  value={inv.url}
                  onFocus={(e) => e.target.select()}
                  style={{
                    width: '100%',
                    padding: '8px 36px 8px 10px',
                    border: `1px solid ${border}`,
                    borderRadius: 6,
                    backgroundColor: inputBg,
                    color: 'var(--color-text)',
                    fontFamily: '"Geist Mono Variable", ui-monospace, SFMono-Regular, Menlo, monospace',
                    fontSize: 12,
                  }}
                />
                <button
                  type="button"
                  onClick={() => copyURL(inv.url)}
                  aria-label={copiedURL === inv.url ? 'Copied!' : 'Copy link'}
                  title={copiedURL === inv.url ? 'Copied!' : 'Copy link'}
                  style={{
                    position: 'absolute',
                    top: '50%',
                    right: 8,
                    transform: 'translateY(-50%)',
                    padding: 4,
                    background: 'none',
                    border: 'none',
                    cursor: 'pointer',
                    fontSize: 14,
                    color: copiedURL === inv.url ? '#34d399' : 'var(--color-text-muted)',
                  }}
                >
                  {copiedURL === inv.url ? '✓' : '⧉'}
                </button>
              </div>
            </div>
          ))}
        </div>

        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <button
            type="button"
            onClick={continueToNext}
            style={{
              padding: '10px 20px',
              border: 'none',
              borderRadius: 8,
              backgroundColor: 'var(--color-accent)',
              color: 'var(--color-text-on-dark)',
              fontWeight: 600,
              fontSize: 14,
              cursor: 'pointer',
            }}
          >
            Continue to AWS account
          </button>
        </div>
      </div>
    );
  }

  return (
    <div>
      <h1 style={{ color: 'var(--color-text)', fontSize: 26, fontWeight: 700, margin: 0, marginBottom: 8 }}>
        Invite your members
      </h1>
      <p style={{ color: 'var(--color-text-mid)', fontSize: 14, marginTop: 0, marginBottom: 24 }}>
        AxiaOps doesn't send invitation emails — after you submit, you'll
        get a link for each invite to share over Slack, email, or another
        private channel. You can also skip this and invite people later
        from Settings → Members.
      </p>

      {rows.map((row) => (
        <div key={row.id} style={{ display: 'flex', gap: 8, marginBottom: 10 }}>
          <input
            type="email"
            // type="email" alone accepts "alice@example" (no TLD) per HTML5.
            // The pattern requires a "." in the domain to reject typos like
            // "alice@test.com" → "alice@test". Backend enforces the same
            // rule via model.ValidateInvitableEmail.
            pattern="[^\s@]+@[^\s@]+\.[^\s@]+"
            title="Enter an email like alice@example.com"
            value={row.email}
            onChange={(e) => updateRow(row.id, { email: e.target.value })}
            placeholder="name@example.com"
            style={{
              flex: 2,
              padding: '8px 10px',
              border: `1px solid ${border}`,
              borderRadius: 6,
              backgroundColor: inputBg,
              color: 'var(--color-text)',
              fontSize: 13,
            }}
          />
          <select
            value={row.role}
            onChange={(e) => updateRow(row.id, { role: e.target.value })}
            style={{
              flex: 1,
              padding: '8px 10px',
              border: `1px solid ${border}`,
              borderRadius: 6,
              backgroundColor: inputBg,
              color: 'var(--color-text)',
              fontSize: 13,
            }}
          >
            {ROLES.map((r) => (
              <option key={r.value} value={r.value}>{r.label}</option>
            ))}
          </select>
          {rows.length > 1 && (
            <button
              type="button"
              onClick={() => removeRow(row.id)}
              style={{
                padding: '0 10px',
                border: `1px solid ${border}`,
                borderRadius: 6,
                backgroundColor: 'transparent',
                color: 'var(--color-text-muted)',
                cursor: 'pointer',
                fontSize: 16,
              }}
              aria-label="Remove invitation row"
            >
              ×
            </button>
          )}
        </div>
      ))}

      <button
        type="button"
        onClick={addRow}
        style={{
          padding: '6px 12px',
          border: `1px dashed ${border}`,
          borderRadius: 6,
          backgroundColor: 'transparent',
          color: 'var(--color-text-mid)',
          fontSize: 12,
          cursor: 'pointer',
          marginBottom: 24,
        }}
      >
        + Add another
      </button>

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <button
          type="button"
          onClick={skip}
          disabled={sending}
          style={{
            padding: '10px 16px',
            border: 'none',
            borderRadius: 8,
            backgroundColor: 'transparent',
            color: 'var(--color-text-muted)',
            fontSize: 14,
            cursor: 'pointer',
          }}
        >
          Skip for now
        </button>
        <button
          type="button"
          onClick={sendAll}
          disabled={sending}
          style={{
            padding: '10px 20px',
            border: 'none',
            borderRadius: 8,
            backgroundColor: 'var(--color-accent)',
            color: 'var(--color-text-on-dark)',
            fontWeight: 600,
            fontSize: 14,
            cursor: sending ? 'not-allowed' : 'pointer',
            opacity: sending ? 0.5 : 1,
          }}
        >
          {sending ? 'Sending…' : 'Send invitations'}
        </button>
      </div>
    </div>
  );
}
