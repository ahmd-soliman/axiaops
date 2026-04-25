import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTheme } from '../theme/ThemeContext';
import { useMe } from '../context/MeContext';
import {
  inviteMember,
  listMemberships,
  removeMember,
  transferOwnership,
  updateMemberRole,
} from '../api/client';
import { Spinner } from '../components/primitives';

// Role labels in the order shown in dropdowns and the matrix in the design.
// Owner is intentionally omitted — promotion to owner happens only via the
// transfer-ownership flow.
const ASSIGNABLE_ROLES = ['admin', 'member', 'viewer'];

export default function Users() {
  const { theme: t, isDark } = useTheme();
  const { me, can, refresh } = useMe();
  const qc = useQueryClient();

  const [inviteEmail, setInviteEmail] = useState('');
  const [inviteRole, setInviteRole] = useState('member');
  const [inviteError, setInviteError] = useState('');
  const [error, setError] = useState('');
  const [transferTo, setTransferTo] = useState('');

  const memberships = useQuery({ queryKey: ['memberships'], queryFn: listMemberships });

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['memberships'] });
    refresh();
  };

  const inviteMutation = useMutation({
    mutationFn: ({ email, role }) => inviteMember(email, role),
    onSuccess: () => {
      setInviteEmail('');
      setInviteRole('member');
      setInviteError('');
      invalidate();
    },
    onError: (err) => setInviteError(humanize(err, 'Failed to add user')),
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

  const canInvite = can('members:invite');
  const canManageBasic = can('members:manage_basic');
  const canManageAdmin = can('members:manage_admin');
  const canTransfer = can('tenant:transfer');

  return (
    <div style={{ padding: 24, color: t.textMid }}>
      <h1 style={{ margin: 0, color: t.text, fontSize: 22, fontWeight: 700 }}>Users</h1>
      <p style={{ marginTop: 4, marginBottom: 24, color: t.textMuted, fontSize: 13 }}>
        Manage the people in this AxiaOps tenant.
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
            Add a member
          </h2>
          <p style={{ marginTop: 0, marginBottom: 12, fontSize: 12, color: t.textMuted }}>
            The user must have logged in to AxiaOps at least once before they can be added.
          </p>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (!inviteEmail.trim()) return;
              inviteMutation.mutate({ email: inviteEmail.trim(), role: inviteRole });
            }}
            style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}
          >
            <input
              type="email"
              required
              value={inviteEmail}
              onChange={(e) => setInviteEmail(e.target.value)}
              placeholder="user@example.com"
              style={inputStyle(t)}
            />
            <select value={inviteRole} onChange={(e) => setInviteRole(e.target.value)} style={inputStyle(t)}>
              {ASSIGNABLE_ROLES.filter((r) => r !== 'admin' || canManageAdmin).map((r) => (
                <option key={r} value={r}>{r}</option>
              ))}
            </select>
            <button type="submit" disabled={inviteMutation.isPending} style={primaryButton(t)}>
              {inviteMutation.isPending ? 'Adding…' : 'Add'}
            </button>
          </form>
          {inviteError && (
            <p style={{ marginTop: 8, marginBottom: 0, fontSize: 12, color: '#ef4444' }}>{inviteError}</p>
          )}
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
                              ? 'Leave this tenant?'
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
            Promote another tenant member to owner. You'll be demoted to admin in the same operation.
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
