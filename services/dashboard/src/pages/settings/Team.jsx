import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTheme } from '../../theme/ThemeContext';
import { useMe } from '../../context/MeContext';
import {
  addMember,
  createInvitation,
  listInvitations,
  listMemberships,
  removeMember,
  revokeInvitation,
  transferOwnership,
  updateMemberRole,
} from '../../api/client';
import { PERM } from '../../api/permissions';
import { Spinner } from '../../components/primitives';

// Role labels in the order shown in dropdowns and the matrix in the design.
// Owner is intentionally omitted — promotion to owner happens only via the
// transfer-ownership flow.
const ASSIGNABLE_ROLES = ['admin', 'member', 'viewer'];

export default function Team() {
  const { theme: t, isDark } = useTheme();
  const { me, can, refresh } = useMe();
  const qc = useQueryClient();

  const [addEmail, setAddEmail] = useState('');
  const [addRole, setAddRole] = useState('member');
  const [addError, setAddError] = useState('');
  const [error, setError] = useState('');
  const [transferTo, setTransferTo] = useState('');
  // Most-recently-issued invitation, surfaced inline so the admin can copy
  // the OOB redemption URL. The API returns redemption_url under
  // AUTH_PROVIDER=native|both; absent under AUTH_PROVIDER=kinde where Kinde
  // sends the email and the admin doesn't need to share a link manually.
  const [lastInvite, setLastInvite] = useState(null);
  const [copied, setCopied] = useState(false);

  const memberships = useQuery({ queryKey: ['memberships'], queryFn: listMemberships });
  const invitations = useQuery({ queryKey: ['invitations'], queryFn: () => listInvitations('pending') });

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['memberships'] });
    qc.invalidateQueries({ queryKey: ['invitations'] });
    refresh();
  };

  // Email-first invite. Tries POST /v1/invitations, falls back to
  // POST /v1/memberships if the API reports the user already exists without
  // a membership (the user logged in once but was never added).
  const addMutation = useMutation({
    mutationFn: async ({ email, role }) => {
      try {
        const result = await createInvitation(email, role);
        return { ...result, _email: email, _role: role };
      } catch (err) {
        if (err?.body?.error === 'user_exists_use_memberships') {
          return await addMember(email, role);
        }
        throw err;
      }
    },
    onSuccess: (data) => {
      setAddEmail('');
      setAddRole('member');
      setAddError('');
      setCopied(false);
      // Surface the redemption URL inline so the admin can share it OOB.
      // Absent under AUTH_PROVIDER=kinde (Kinde sends an email itself) and
      // also absent on the addMember fallback (existing-user-no-membership).
      if (data?.redemption_url) {
        // The API emits a relative path (`/accept-invite?token=...`) when
        // PUBLIC_HOST is unset (typical for local dev) and an absolute URL
        // when it is. Resolve to absolute against window.location.origin so
        // the OOB-shared link is always usable in any chat/email client. See
        // services/api/internal/api/invitations.go:buildRedemptionURL.
        const url = data.redemption_url.startsWith('/')
          ? window.location.origin + data.redemption_url
          : data.redemption_url;
        setLastInvite({
          email: data._email || data.email,
          role: data._role || data.role,
          url,
        });
      } else {
        setLastInvite(null);
      }
      invalidate();
    },
    onError: (err) => setAddError(humanize(err, 'Failed to invite user')),
  });

  const revokeInvitationMutation = useMutation({
    mutationFn: (id) => revokeInvitation(id),
    onSuccess: invalidate,
    onError: (err) => setError(humanize(err, 'Failed to revoke invitation')),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, role }) => updateMemberRole(id, role),
    onSuccess: invalidate,
    onError: (err) => setError(humanize(err, 'Failed to change role')),
  });

  const removeMutation = useMutation({
    mutationFn: (id) => removeMember(id),
    onSuccess: invalidate,
    onError: (err) => setError(humanize(err, 'Failed to remove member')),
  });

  const transferMutation = useMutation({
    mutationFn: (toUserID) => transferOwnership(toUserID),
    onSuccess: () => {
      setTransferTo('');
      invalidate();
    },
    onError: (err) => setError(humanize(err, 'Failed to transfer ownership')),
  });

  const canInvite = can(PERM.MEMBERS_INVITE);
  const canManageBasic = can(PERM.MEMBERS_MANAGE_BASIC);
  const canManageAdmin = can(PERM.MEMBERS_MANAGE_ADMIN);
  const canTransfer = can(PERM.ORGANIZATION_TRANSFER);

  return (
    <div style={{ padding: 24, color: t.textMid }}>
      <h1 style={{ margin: 0, color: t.text, fontSize: 22, fontWeight: 700 }}>Team</h1>
      <p style={{ marginTop: 4, marginBottom: 24, color: t.textMuted, fontSize: 13 }}>
        Manage the people in your organization.
      </p>

      {error && (
        <Banner color={isDark ? '#fca5a5' : '#b91c1c'} bg={isDark ? 'rgba(239,68,68,0.15)' : '#fee2e2'}>
          {error}
        </Banner>
      )}

      {canInvite && (
        <section
          style={{
            border: `1px solid ${t.border}`,
            borderRadius: 8,
            padding: 16,
            marginBottom: 24,
            backgroundColor: t.surface,
          }}
        >
          <h2 style={{ margin: 0, marginBottom: 12, fontSize: 14, fontWeight: 700, color: t.text }}>
            Invite a teammate
          </h2>
          <p style={{ marginTop: 0, marginBottom: 12, fontSize: 12, color: t.textMuted }}>
            Generates an invitation link. Copy it and share with the user out of
            band (Slack, email, etc). They join with the role you pick on first
            sign-in.
          </p>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (!addEmail.trim()) return;
              addMutation.mutate({ email: addEmail.trim(), role: addRole });
            }}
            style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}
          >
            <input
              type="email"
              required
              value={addEmail}
              onChange={(e) => setAddEmail(e.target.value)}
              placeholder="user@example.com"
              style={inputStyle(t)}
            />
            <select value={addRole} onChange={(e) => setAddRole(e.target.value)} style={inputStyle(t)}>
              {ASSIGNABLE_ROLES.filter((r) => r !== 'admin' || canManageAdmin).map((r) => (
                <option key={r} value={r}>{r}</option>
              ))}
            </select>
            <button type="submit" disabled={addMutation.isPending} style={primaryButton(t)}>
              {addMutation.isPending ? 'Sending…' : 'Create invite link'}
            </button>
          </form>
          {addError && (
            <p style={{ marginTop: 8, marginBottom: 0, fontSize: 12, color: '#ef4444' }}>{addError}</p>
          )}

          {lastInvite && (
            <div
              style={{
                marginTop: 12,
                padding: 12,
                borderRadius: 6,
                backgroundColor: isDark ? 'rgba(34,197,94,0.10)' : '#ecfdf5',
                border: `1px solid ${isDark ? 'rgba(34,197,94,0.35)' : '#86efac'}`,
              }}
            >
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 8, marginBottom: 8 }}>
                <span style={{ fontSize: 12, fontWeight: 600, color: t.text }}>
                  Invitation created for <strong>{lastInvite.email}</strong> ({lastInvite.role})
                </span>
                <button
                  type="button"
                  onClick={() => { setLastInvite(null); setCopied(false); }}
                  aria-label="Dismiss"
                  style={{
                    background: 'none',
                    border: 'none',
                    color: t.textMuted,
                    fontSize: 16,
                    cursor: 'pointer',
                    padding: 0,
                    lineHeight: 1,
                  }}
                >
                  ×
                </button>
              </div>
              <div style={{ position: 'relative' }}>
                <input
                  type="text"
                  readOnly
                  value={lastInvite.url}
                  onFocus={(e) => e.target.select()}
                  style={{
                    ...inputStyle(t),
                    width: '100%',
                    paddingRight: 36,
                    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
                    fontSize: 12,
                  }}
                />
                <button
                  type="button"
                  onClick={async () => {
                    try {
                      await navigator?.clipboard?.writeText(lastInvite.url);
                      setCopied(true);
                      setTimeout(() => setCopied(false), 2000);
                    } catch {
                      // Clipboard API unavailable (insecure context, etc).
                      // Focus auto-selects the input so Cmd/Ctrl-C still works.
                    }
                  }}
                  aria-label={copied ? 'Copied!' : 'Copy link'}
                  title={copied ? 'Copied!' : 'Copy link'}
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
                    color: copied ? '#34d399' : t.textMuted,
                  }}
                >
                  {copied ? '✓' : '⧉'}
                </button>
              </div>
              <p style={{ marginTop: 8, marginBottom: 0, fontSize: 11, color: t.textMuted }}>
                Anyone with this link can redeem the invitation, so share over
                a private channel. Revoke it from the Pending invitations table
                below if needed.
              </p>
            </div>
          )}
        </section>
      )}

      {/* Pending invitations — visible to anyone with members:read. */}
      {(invitations.data?.length || 0) > 0 && (
        <section
          style={{
            border: `1px solid ${t.border}`,
            borderRadius: 8,
            backgroundColor: t.surface,
            marginBottom: 24,
            overflow: 'hidden',
          }}
        >
          <div style={{ padding: '12px 16px', borderBottom: `1px solid ${t.border}`, backgroundColor: t.surfaceRaised }}>
            <h2 style={{ margin: 0, fontSize: 14, fontWeight: 700, color: t.text }}>Pending invitations</h2>
          </div>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr style={{ borderBottom: `1px solid ${t.border}`, backgroundColor: t.surface }}>
                <Th t={t}>Email</Th>
                <Th t={t}>Role</Th>
                <Th t={t}>Invited</Th>
                <Th t={t}></Th>
              </tr>
            </thead>
            <tbody>
              {(invitations.data || []).map((inv) => (
                <tr key={inv.id} style={{ borderBottom: `1px solid ${t.border}` }}>
                  <Td t={t}>{inv.email}</Td>
                  <Td t={t}>{inv.role}</Td>
                  <Td t={t}>{new Date(inv.created_at).toLocaleDateString()}</Td>
                  <Td t={t}>
                    {canInvite && (inv.role !== 'admin' || canManageAdmin) && (
                      <button
                        type="button"
                        onClick={() => revokeInvitationMutation.mutate(inv.id)}
                        disabled={revokeInvitationMutation.isPending}
                        style={{
                          padding: '4px 10px',
                          border: `1px solid ${t.border}`,
                          borderRadius: 4,
                          backgroundColor: 'transparent',
                          color: '#ef4444',
                          fontSize: 12,
                          cursor: 'pointer',
                        }}
                      >
                        Revoke
                      </button>
                    )}
                  </Td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      <section
        style={{
          border: `1px solid ${t.border}`,
          borderRadius: 8,
          backgroundColor: t.surface,
          overflow: 'hidden',
        }}
      >
        {memberships.isPending ? (
          <div style={{ padding: 32, textAlign: 'center' }}><Spinner /></div>
        ) : memberships.isError ? (
          <div style={{ padding: 24, color: '#ef4444' }}>Failed to load members.</div>
        ) : (
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr style={{ borderBottom: `1px solid ${t.border}`, backgroundColor: t.surfaceRaised }}>
                <Th t={t}>Email</Th>
                <Th t={t}>Name</Th>
                <Th t={t}>Role</Th>
                <Th t={t}>Joined</Th>
                <Th t={t}></Th>
              </tr>
            </thead>
            <tbody>
              {(memberships.data || []).map((m) => {
                const isSelf = me && m.user_id === me.user_id;
                const targetIsElevated = m.role === 'admin' || m.role === 'owner';
                const allowEdit =
                  !isSelf && m.role !== 'owner' && (
                    targetIsElevated ? canManageAdmin : canManageBasic
                  );
                const allowRemove =
                  isSelf
                    ? m.role !== 'owner' // self-leave; owners must transfer first
                    : m.role !== 'owner' && (targetIsElevated ? canManageAdmin : canManageBasic);
                return (
                  <tr key={m.id} style={{ borderBottom: `1px solid ${t.border}` }}>
                    <Td t={t}>{m.email || '—'}</Td>
                    <Td t={t}>{m.name || '—'}</Td>
                    <Td t={t}>
                      {allowEdit ? (
                        <select
                          value={m.role}
                          onChange={(e) => updateMutation.mutate({ id: m.id, role: e.target.value })}
                          style={inputStyle(t)}
                        >
                          {ASSIGNABLE_ROLES.filter((r) => r !== 'admin' || canManageAdmin).map((r) => (
                            <option key={r} value={r}>{r}</option>
                          ))}
                        </select>
                      ) : (
                        <span style={roleBadge(m.role, t, isDark)}>{m.role}</span>
                      )}
                    </Td>
                    <Td t={t}>{formatDate(m.created_at)}</Td>
                    <Td t={t}>
                      {allowRemove && (
                        <button
                          type="button"
                          onClick={() => {
                            const confirmText = isSelf
                              ? 'Leave this organization?'
                              : `Remove ${m.email || 'this user'}?`;
                            if (window.confirm(confirmText)) removeMutation.mutate(m.id);
                          }}
                          style={dangerButton(t)}
                        >
                          {isSelf ? 'Leave' : 'Remove'}
                        </button>
                      )}
                    </Td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </section>

      {canTransfer && (
        <section
          style={{
            marginTop: 24,
            border: `1px solid ${t.border}`,
            borderRadius: 8,
            padding: 16,
            backgroundColor: t.surface,
          }}
        >
          <h2 style={{ margin: 0, marginBottom: 8, fontSize: 14, fontWeight: 700, color: t.text }}>
            Transfer ownership
          </h2>
          <p style={{ marginTop: 0, marginBottom: 12, fontSize: 12, color: t.textMuted }}>
            Promote another member to owner. You'll be demoted to admin in the same operation.
          </p>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <select
              value={transferTo}
              onChange={(e) => setTransferTo(e.target.value)}
              style={inputStyle(t)}
            >
              <option value="">Select user…</option>
              {(memberships.data || [])
                .filter((m) => m.role !== 'owner' && (!me || m.user_id !== me.user_id))
                .map((m) => (
                  <option key={m.user_id} value={m.user_id}>
                    {m.email || m.user_id} ({m.role})
                  </option>
                ))}
            </select>
            <button
              type="button"
              disabled={!transferTo || transferMutation.isPending}
              onClick={() => {
                if (window.confirm('Transfer ownership? You will be demoted to admin.')) {
                  transferMutation.mutate(transferTo);
                }
              }}
              style={primaryButton(t)}
            >
              {transferMutation.isPending ? 'Transferring…' : 'Transfer'}
            </button>
          </div>
        </section>
      )}
    </div>
  );
}

// ── Visual helpers ──────────────────────────────────────────────────────────

function Banner({ children, color, bg }) {
  return (
    <div style={{ padding: '8px 12px', marginBottom: 16, borderRadius: 6, color, backgroundColor: bg, fontSize: 13 }}>
      {children}
    </div>
  );
}

function Th({ t, children }) {
  return (
    <th style={{
      padding: '10px 12px',
      textAlign: 'left',
      fontWeight: 600,
      fontSize: 12,
      color: t.textMuted,
      letterSpacing: 0.3,
    }}>{children}</th>
  );
}

function Td({ t, children }) {
  return <td style={{ padding: '10px 12px', color: t.text }}>{children}</td>;
}

function inputStyle(t) {
  return {
    padding: '6px 10px',
    border: `1px solid ${t.border}`,
    borderRadius: 6,
    fontSize: 13,
    backgroundColor: t.bg,
    color: t.text,
  };
}

function primaryButton(t) {
  return {
    padding: '7px 14px',
    border: 'none',
    borderRadius: 6,
    backgroundColor: t.accent,
    color: '#fff',
    fontWeight: 600,
    fontSize: 13,
    cursor: 'pointer',
  };
}

function dangerButton(t) {
  return {
    padding: '5px 10px',
    border: `1px solid ${t.border}`,
    borderRadius: 6,
    backgroundColor: 'transparent',
    color: '#ef4444',
    fontSize: 12,
    fontWeight: 600,
    cursor: 'pointer',
  };
}

function roleBadge(role, t, isDark) {
  const palette = {
    owner:  { fg: '#7c3aed', bg: isDark ? 'rgba(124,58,237,0.15)' : '#ede9fe' },
    admin:  { fg: '#3b82f6', bg: isDark ? 'rgba(59,130,246,0.15)' : '#dbeafe' },
    member: { fg: '#10b981', bg: isDark ? 'rgba(16,185,129,0.15)' : '#d1fae5' },
    viewer: { fg: t.textMuted, bg: t.surfaceRaised },
  }[role] || { fg: t.textMuted, bg: t.surfaceRaised };
  return {
    display: 'inline-block',
    padding: '2px 8px',
    borderRadius: 4,
    fontSize: 11,
    fontWeight: 600,
    color: palette.fg,
    backgroundColor: palette.bg,
    letterSpacing: 0.2,
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
  if (err.status === 404) return 'User has not logged in to AxiaOps yet.';
  if (err.status === 409) return err.body || 'Conflict — last owner cannot be demoted; transfer ownership first.';
  if (err.status === 403) return 'You do not have permission for that change.';
  if (err.status === 400) return err.body || 'Invalid request.';
  return err.message || fallback;
}
