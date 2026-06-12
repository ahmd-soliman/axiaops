import { useState, useRef } from 'react';
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
import { useBreakpoint } from '../../components/primitives/useBreakpoint';
import { CardRow } from '../../components/primitives/CardRow';
import { useDestructiveConfirm, DestructiveConfirmModal } from '../../components/DestructiveConfirm';
import { useToast } from '../../context/ToastContext';
import { formatDate as formatFullDate } from '../../utils/formatDate';

// Role labels in the order shown in dropdowns and the matrix in the design.
// Owner is intentionally omitted — promotion to owner happens only via the
// transfer-ownership flow.
const ASSIGNABLE_ROLES = ['admin', 'member', 'viewer'];

export default function Members() {
  const { isDark } = useTheme();
  const { me, can, refresh } = useMe();
  const { toast } = useToast();
  const qc = useQueryClient();
  const { isAtMost } = useBreakpoint();
  const isMobile = isAtMost('sm');

  const [addEmail, setAddEmail] = useState('');
  const [addRole, setAddRole] = useState('member');
  const [addError, setAddError] = useState('');
  const [error, setError] = useState('');
  const [transferTo, setTransferTo] = useState('');
  // Target row for the remove-member confirm modal. Shared across the card
  // and table layouts; one modal instance, two triggers. Self-leave uses the
  // literal "leave" as the type-to-confirm target (member emails are not
  // shown to users typing their own removal).
  const [removeTarget, setRemoveTarget] = useState(null);
  // Most-recently-issued invitation, surfaced inline so the admin can copy
  // the OOB redemption URL.
  const [lastInvite, setLastInvite] = useState(null);
  const [copied, setCopied] = useState(false);

  const memberships = useQuery({ queryKey: ['memberships'], queryFn: listMemberships });
  const invitations = useQuery({ queryKey: ['invitations'], queryFn: () => listInvitations('pending') });

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['memberships'] });
    qc.invalidateQueries({ queryKey: ['invitations'] });
    refresh();
  };

  // Synchronous double-submit guard. `addMutation.isPending` only flips after
  // the dispatch + a re-render, so two Enter presses in the same tick could
  // both fire createInvitation before the button disables. This ref is set
  // synchronously in the submit handler and cleared in onSettled.
  const invitingRef = useRef(false);

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
      // Absent on the addMember fallback (existing-user-no-membership).
      if (data?.redemption_url) {
        // The API emits a relative path (`/accept-invite?token=...`) when
        // PUBLIC_HOST is unset (typical for local dev) and an absolute URL
        // when it is. Resolve to absolute against window.location.origin so
        // the OOB-shared link is always usable in any chat/email client. See
        // services/api/internal/api/invitations.go:buildRedemptionURL.
        //
        // Defense-in-depth: only accept either a clean same-origin path
        // (single leading "/") OR an absolute URL on our own origin. A
        // protocol-relative shape ("//evil.com/...") is dropped — currently
        // unreachable from the server's buildRedemptionURL but a
        // future-proofing guard against bypassing the open-redirect-style
        // checks via the invitation flow.
        const raw = data.redemption_url;
        let url = null;
        if (raw.startsWith('//')) {
          // Protocol-relative — reject.
        } else if (raw.startsWith('/')) {
          url = window.location.origin + raw;
        } else {
          try {
            const parsed = new URL(raw);
            if (parsed.origin === window.location.origin) {
              url = parsed.toString();
            }
          } catch {
            // Malformed URL — drop.
          }
        }
        if (url) {
          setLastInvite({
            email: data._email || data.email,
            role: data._role || data.role,
            url,
            // Best-effort invite-email outcome (services/api invitations.go):
            // sent | failed | skipped_no_transport | skipped_no_public_host |
            // error, or '' when no mailer is wired. Drives the headline + the
            // "emailed vs share-the-link" framing below. The addMember
            // fallback path has no email_delivery, so it reads as '' (neutral).
            emailDelivery: data.email_delivery || '',
            // Invitation expiry (INVITATION_TTL_DAYS, default 14d). Surfaced in
            // the box so the admin knows the link's shelf life — the email
            // already states it (invite_email.go), the UI shouldn't be silent.
            expiresAt: data.expires_at || '',
            // Tasks.md row 2.7.20: yellow callout when the org gates on
            // SSO. The URL still works for the redemption hop, but every
            // authed request afterward 403s with `sso_required` because
            // EnforceSSO blocks password sessions in such orgs. The
            // admin needs to know this is a break-glass, not the happy
            // path. `enforcement_hint` is "sso_required" or absent.
            enforcementHint: data.enforcement_hint || '',
          });
        } else {
          setLastInvite(null);
        }
      } else {
        setLastInvite(null);
      }
      invalidate();
    },
    onError: (err) => setAddError(humanize(err, 'Failed to invite user')),
    onSettled: () => { invitingRef.current = false; },
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

  // Destructive-action confirm modals — type-to-confirm UX shared with other
  // pages (org delete, account delete, user delete). Replaces three earlier
  // window.confirm() sites (per issue #84). The hook auto-closes itself on
  // success, so onSuccess here only clears the page-local target state.
  const removeCtrl = useDestructiveConfirm({
    target: removeTarget?.isSelf ? 'leave' : (removeTarget?.email || ''),
    mutationFn: () => removeMember(removeTarget?.id),
    successMessage: removeTarget?.isSelf ? 'Left the organization' : 'Member removed',
    onSuccess: () => {
      setRemoveTarget(null);
      invalidate();
    },
    toast,
  });

  const transferToEmail = (memberships.data || []).find((m) => m.user_id === transferTo)?.email || '';
  const transferCtrl = useDestructiveConfirm({
    target: transferToEmail,
    mutationFn: () => transferOwnership(transferTo),
    successMessage: 'Ownership transferred',
    onSuccess: () => {
      setTransferTo('');
      invalidate();
    },
    toast,
  });

  const canInvite = can(PERM.MEMBERS_INVITE);
  const canManageBasic = can(PERM.MEMBERS_MANAGE_BASIC);
  const canManageAdmin = can(PERM.MEMBERS_MANAGE_ADMIN);
  const canTransfer = can(PERM.ORGANIZATION_TRANSFER);

  return (
    <div style={{ padding: 24, color: 'var(--color-text-mid)' }}>
      <h1 style={{ margin: 0, color: 'var(--color-text)', fontSize: 22, fontWeight: 700 }}>Members</h1>
      <p style={{ marginTop: 4, marginBottom: 24, color: 'var(--color-text-muted)', fontSize: 13 }}>
        Manage the people in your organization.
      </p>

      {error && (
        <Banner color={'var(--color-error)'} bg={isDark ? 'rgba(239,68,68,0.15)' : '#fee2e2'}>
          {error}
        </Banner>
      )}

      {canInvite && (
        <section
          style={{
            border: `1px solid var(--color-border)`,
            borderRadius: 8,
            padding: 16,
            marginBottom: 24,
            backgroundColor: 'var(--color-surface)',
          }}
        >
          <h2 style={{ margin: 0, marginBottom: 12, fontSize: 14, fontWeight: 700, color: 'var(--color-text)' }}>
            Invite a member
          </h2>
          <p style={{ marginTop: 0, marginBottom: 12, fontSize: 12, color: 'var(--color-text-muted)' }}>
            Generates an invitation link. Copy it and share with the user out of
            band (Slack, email, etc). They join with the role you pick on first
            sign-in.
          </p>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (!addEmail.trim()) return;
              if (invitingRef.current) return; // already submitting this tick
              invitingRef.current = true;
              addMutation.mutate({ email: addEmail.trim(), role: addRole });
            }}
            style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}
          >
            <input
              type="email"
              required
              // type="email" alone accepts "alice@example" (no TLD) per HTML5.
              // The pattern requires a "." in the domain to reject typos like
              // "alice@test.com" → "alice@test". Backend enforces the same
              // rule via model.ValidateInvitableEmail.
              pattern="[^\s@]+@[^\s@]+\.[^\s@]+"
              title="Enter an email like alice@example.com"
              value={addEmail}
              onChange={(e) => setAddEmail(e.target.value)}
              placeholder="user@example.com"
              style={inputStyle()}
            />
            <select value={addRole} onChange={(e) => setAddRole(e.target.value)} style={inputStyle()}>
              {ASSIGNABLE_ROLES.filter((r) => r !== 'admin' || canManageAdmin).map((r) => (
                <option key={r} value={r}>{r}</option>
              ))}
            </select>
            <button type="submit" disabled={addMutation.isPending} style={primaryButton()}>
              {addMutation.isPending ? 'Sending…' : 'Create invite link'}
            </button>
          </form>
          {addError && (
            <p style={{ marginTop: 8, marginBottom: 0, fontSize: 12, color: 'var(--color-error)' }}>{addError}</p>
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
                <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text)' }}>
                  {inviteDelivery(lastInvite.emailDelivery).tone === 'sent' && '✓ '}
                  Invitation {inviteDelivery(lastInvite.emailDelivery).verb}{' '}
                  <strong>{lastInvite.email}</strong> ({lastInvite.role})
                </span>
                <button
                  type="button"
                  onClick={() => { setLastInvite(null); setCopied(false); }}
                  aria-label="Dismiss"
                  style={{
                    background: 'none',
                    border: 'none',
                    color: 'var(--color-text-muted)',
                    fontSize: 16,
                    cursor: 'pointer',
                    padding: 0,
                    lineHeight: 1,
                  }}
                >
                  ×
                </button>
              </div>
              {/* 3a — surface the best-effort email outcome. When the email
                  couldn't be sent, an amber note tells the admin to fall back
                  to sharing the link directly. */}
              {inviteDelivery(lastInvite.emailDelivery).note && (
                <div
                  style={{
                    marginBottom: 8,
                    padding: 8,
                    borderRadius: 4,
                    backgroundColor: isDark ? 'rgba(234,179,8,0.12)' : '#fef9c3',
                    border: `1px solid ${isDark ? 'rgba(234,179,8,0.35)' : '#fde047'}`,
                    color: isDark ? '#fde68a' : '#854d0e',
                    fontSize: 11,
                    lineHeight: 1.5,
                  }}
                >
                  {inviteDelivery(lastInvite.emailDelivery).note}
                </div>
              )}
              {lastInvite.enforcementHint === 'sso_required' && (
                <div
                  style={{
                    marginBottom: 8,
                    padding: 8,
                    borderRadius: 4,
                    backgroundColor: isDark ? 'rgba(234,179,8,0.12)' : '#fef9c3',
                    border: `1px solid ${isDark ? 'rgba(234,179,8,0.35)' : '#fde047'}`,
                    color: isDark ? '#fde68a' : '#854d0e',
                    fontSize: 11,
                    lineHeight: 1.5,
                  }}
                >
                  <strong>SSO is enforced for this organization.</strong> The
                  invitee will be auto-onboarded on first SSO login — ask them
                  to sign in via your SSO provider instead of clicking this
                  link. The URL still works as a break-glass for cross-org or
                  IdP-outage cases, but the password-based session it mints
                  will be blocked on the next request.
                </div>
              )}
              {/* 3c — when the email was sent the link is a secondary "share
                  another way" option; otherwise it's the primary handoff. */}
              <p style={{ marginTop: 0, marginBottom: 6, fontSize: 11, color: 'var(--color-text-mid)' }}>
                {inviteDelivery(lastInvite.emailDelivery).linkIntro}
              </p>
              <div style={{ position: 'relative' }}>
                <input
                  type="text"
                  readOnly
                  value={lastInvite.url}
                  onFocus={(e) => e.target.select()}
                  style={{
                    ...inputStyle(),
                    width: '100%',
                    paddingRight: 36,
                    fontFamily: '"Geist Mono Variable", ui-monospace, SFMono-Regular, Menlo, monospace',
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
                    color: copied ? '#34d399' : 'var(--color-text-muted)',
                  }}
                >
                  {copied ? '✓' : '⧉'}
                </button>
              </div>
              {lastInvite.expiresAt && (
                <p style={{ marginTop: 8, marginBottom: 0, fontSize: 11, color: 'var(--color-text-mid)' }}>
                  This link expires on <strong>{formatDate(lastInvite.expiresAt)}</strong>.
                </p>
              )}
              <p style={{ marginTop: 6, marginBottom: 0, fontSize: 11, color: 'var(--color-text-muted)' }}>
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
            border: `1px solid var(--color-border)`,
            borderRadius: 8,
            backgroundColor: 'var(--color-surface)',
            marginBottom: 24,
            overflow: 'hidden',
          }}
        >
          <div style={{ padding: '12px 16px', borderBottom: `1px solid var(--color-border)`, backgroundColor: 'var(--color-surface-raised)' }}>
            <h2 style={{ margin: 0, fontSize: 14, fontWeight: 700, color: 'var(--color-text)' }}>Pending invitations</h2>
          </div>
          {isMobile ? (
            // Phone layout — drop the <table> for a stacked card list.
            // <table>'s columnar layout doesn't reflow; on a 375px viewport
            // the 4-column header forces horizontal scroll. Cards keep the
            // same data + actions accessible without a side-scroll gesture.
            <div style={{ padding: 12, display: 'flex', flexDirection: 'column', gap: 8 }}>
              {(invitations.data || []).map((inv) => {
                const canRevoke = canInvite && (inv.role !== 'admin' || canManageAdmin);
                return (
                  <CardRow
                    key={inv.id}
                    header={
                      <>
                        <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                          {inv.email}
                        </span>
                        <span style={roleBadge()}>{inv.role}</span>
                      </>
                    }
                    body={
                      <span style={{ fontSize: 12, color: 'var(--color-text-muted)' }}>
                        Invited {formatDate(inv.created_at)}
                      </span>
                    }
                    actions={canRevoke ? (
                      <button
                        type="button"
                        onClick={() => revokeInvitationMutation.mutate(inv.id)}
                        disabled={revokeInvitationMutation.isPending}
                        style={{ ...dangerButton(), minHeight: 36 }}
                      >
                        Revoke
                      </button>
                    ) : null}
                  />
                );
              })}
            </div>
          ) : (
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr style={{ borderBottom: `1px solid var(--color-border)`, backgroundColor: 'var(--color-surface)' }}>
                  <Th>Email</Th>
                  <Th>Role</Th>
                  <Th>Invited</Th>
                  <Th></Th>
                </tr>
              </thead>
              <tbody>
                {(invitations.data || []).map((inv) => (
                  <tr key={inv.id} style={{ borderBottom: `1px solid var(--color-border)` }}>
                    <Td>{inv.email}</Td>
                    <Td>{inv.role}</Td>
                    <Td>{formatDate(inv.created_at)}</Td>
                    <Td>
                      {canInvite && (inv.role !== 'admin' || canManageAdmin) && (
                        <button
                          type="button"
                          onClick={() => revokeInvitationMutation.mutate(inv.id)}
                          disabled={revokeInvitationMutation.isPending}
                          style={{
                            padding: '4px 10px',
                            border: `1px solid var(--color-border)`,
                            borderRadius: 4,
                            backgroundColor: 'transparent',
                            color: 'var(--color-error)',
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
          )}
        </section>
      )}

      <section
        style={{
          border: `1px solid var(--color-border)`,
          borderRadius: 8,
          backgroundColor: 'var(--color-surface)',
          overflow: 'hidden',
        }}
      >
        {memberships.isPending ? (
          <div style={{ padding: 32, textAlign: 'center' }}><Spinner /></div>
        ) : memberships.isError ? (
          <div style={{ padding: 24, color: 'var(--color-error)' }}>Failed to load members.</div>
        ) : isMobile ? (
          // Mobile card stack — six-column membership table doesn't reflow
          // on phones (Email + Name + Role + Joined-via + Joined + Action).
          // Cards retain every field and action; the role select stretches
          // to the row width so it stays tappable.
          <div style={{ padding: 12, display: 'flex', flexDirection: 'column', gap: 8 }}>
            {(memberships.data || []).map((m) => {
              const isSelf = me && m.user_id === me.user_id;
              const targetIsElevated = m.role === 'admin' || m.role === 'owner';
              const allowEdit =
                !isSelf && m.role !== 'owner' && (
                  targetIsElevated ? canManageAdmin : canManageBasic
                );
              const allowRemove =
                isSelf
                  ? m.role !== 'owner'
                  : m.role !== 'owner' && (targetIsElevated ? canManageAdmin : canManageBasic);
              return (
                <CardRow
                  key={m.id}
                  header={
                    <>
                      <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0 }}>
                        {m.email || '—'}
                      </span>
                      {!allowEdit && (
                        <span style={roleBadge()}>{m.role}</span>
                      )}
                    </>
                  }
                  body={
                    <>
                      {m.name && <span style={{ color: 'var(--color-text)' }}>{m.name}</span>}
                      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                        <span style={provenanceBadge()}>
                          {provenanceLabel(m.provisioned_via)}
                        </span>
                        <span style={{ color: 'var(--color-text-muted)' }}>
                          Joined {formatDate(m.created_at)}
                        </span>
                      </div>
                      {allowEdit && (
                        <div style={{ marginTop: 4 }}>
                          <span style={{ display: 'block', fontSize: 11, color: 'var(--color-text-muted)', marginBottom: 4, textTransform: 'uppercase', letterSpacing: 0.3 }}>
                            Role
                          </span>
                          <select
                            value={m.role}
                            disabled={updateMutation.isPending}
                            onChange={(e) => updateMutation.mutate({ id: m.id, role: e.target.value })}
                            style={{ ...inputStyle(), width: '100%', minHeight: 40 }}
                          >
                            {ASSIGNABLE_ROLES.filter((r) => r !== 'admin' || canManageAdmin).map((r) => (
                              <option key={r} value={r}>{r}</option>
                            ))}
                          </select>
                        </div>
                      )}
                    </>
                  }
                  actions={allowRemove ? (
                    <button
                      type="button"
                      onClick={() => {
                        setRemoveTarget({ id: m.id, email: m.email || '', isSelf });
                        removeCtrl.openModal();
                      }}
                      style={{ ...dangerButton(), minHeight: 36 }}
                    >
                      {isSelf ? 'Leave' : 'Remove'}
                    </button>
                  ) : null}
                />
              );
            })}
          </div>
        ) : (
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr style={{ borderBottom: `1px solid var(--color-border)`, backgroundColor: 'var(--color-surface-raised)' }}>
                <Th>Email</Th>
                <Th>Name</Th>
                <Th>Role</Th>
                <Th>Joined via</Th>
                <Th>Joined</Th>
                <Th></Th>
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
                  <tr key={m.id} style={{ borderBottom: `1px solid var(--color-border)` }}>
                    <Td>{m.email || '—'}</Td>
                    <Td>{m.name || '—'}</Td>
                    <Td>
                      {allowEdit ? (
                        <select
                          value={m.role}
                          disabled={updateMutation.isPending}
                          onChange={(e) => updateMutation.mutate({ id: m.id, role: e.target.value })}
                          style={inputStyle()}
                        >
                          {ASSIGNABLE_ROLES.filter((r) => r !== 'admin' || canManageAdmin).map((r) => (
                            <option key={r} value={r}>{r}</option>
                          ))}
                        </select>
                      ) : (
                        <span style={roleBadge()}>{m.role}</span>
                      )}
                    </Td>
                    <Td>
                      <span style={provenanceBadge()}>
                        {provenanceLabel(m.provisioned_via)}
                      </span>
                    </Td>
                    <Td>{formatDate(m.created_at)}</Td>
                    <Td>
                      {allowRemove && (
                        <button
                          type="button"
                          onClick={() => {
                            setRemoveTarget({ id: m.id, email: m.email || '', isSelf });
                            removeCtrl.openModal();
                          }}
                          style={dangerButton()}
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
            border: `1px solid var(--color-border)`,
            borderRadius: 8,
            padding: 16,
            backgroundColor: 'var(--color-surface)',
          }}
        >
          <h2 style={{ margin: 0, marginBottom: 8, fontSize: 14, fontWeight: 700, color: 'var(--color-text)' }}>
            Transfer ownership
          </h2>
          <p style={{ marginTop: 0, marginBottom: 12, fontSize: 12, color: 'var(--color-text-muted)' }}>
            Promote another member to owner. You'll be demoted to admin in the same operation.
          </p>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <select
              value={transferTo}
              onChange={(e) => setTransferTo(e.target.value)}
              style={inputStyle()}
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
              disabled={!transferTo || transferCtrl.isPending}
              onClick={() => transferCtrl.openModal()}
              style={primaryButton()}
            >
              {transferCtrl.isPending ? 'Transferring…' : 'Transfer'}
            </button>
          </div>
        </section>
      )}

      <DestructiveConfirmModal
        ctrl={removeCtrl}
        title={removeTarget?.isSelf ? 'Leave organization' : 'Remove member'}
        warning={
          removeTarget?.isSelf
            ? 'You will lose access to this organization and all its data. You can be re-invited by an admin.'
            : `${removeTarget?.email || 'This user'} will lose access to this organization and all its data. They can be re-invited by an admin.`
        }
        targetLabel={removeTarget?.isSelf ? 'word' : 'member email'}
        confirmLabel={removeTarget?.isSelf ? 'Leave' : 'Remove'}
      />
      <DestructiveConfirmModal
        ctrl={transferCtrl}
        title="Transfer ownership"
        warning={
          `${transferToEmail || 'The selected user'} will become the owner of this organization. You will be demoted to admin in the same operation.`
        }
        targetLabel="new owner's email"
        confirmLabel="Transfer"
      />
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

function Th({ children }) {
  return (
    <th style={{
      padding: '10px 12px',
      textAlign: 'left',
      fontWeight: 600,
      fontSize: 12,
      color: 'var(--color-text-muted)',
      letterSpacing: 0.3,
    }}>{children}</th>
  );
}

function Td({ children }) {
  return <td style={{ padding: '10px 12px', color: 'var(--color-text)' }}>{children}</td>;
}

function inputStyle() {
  return {
    padding: '6px 10px',
    border: `1px solid var(--color-border)`,
    borderRadius: 6,
    fontSize: 13,
    backgroundColor: 'var(--color-bg)',
    color: 'var(--color-text)',
  };
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

function dangerButton() {
  return {
    padding: '5px 10px',
    border: `1px solid var(--color-border)`,
    borderRadius: 6,
    backgroundColor: 'transparent',
    color: 'var(--color-error)',
    fontSize: 12,
    fontWeight: 600,
    cursor: 'pointer',
  };
}

function roleBadge() {
  // Inline muted uppercase — same color for every role. Roles are
  // categorical identity, not state; per-role color coding (purple owner,
  // blue admin, etc.) was decorative. The label discriminates; sort the
  // member list by role if hierarchy-at-a-glance matters. Weight matches
  // provenanceBadge (600) so both metadata columns share a visual register.
  return {
    fontSize: 11,
    fontWeight: 600,
    color: 'var(--color-text-mid)',
    letterSpacing: 0.4,
    textTransform: 'uppercase',
  };
}

// provisioned_via mirrors model.ProvisionedVia* in services/shared/model/sso.go.
// Friendly labels disambiguate "this person was invited and accepted" from
// "this person was added directly" / "JIT-created via SSO" / etc.
function provenanceLabel(via) {
  switch (via) {
    case 'invitation': return 'Invite accepted';
    case 'manual':     return 'Added directly';
    case 'jit':        return 'SSO';
    case 'scim':       return 'SCIM';
    case 'bootstrap':  return 'Bootstrap';
    case 'legacy':     return 'Legacy';
    default:           return via || '—';
  }
}

// inviteDelivery maps the API's best-effort email outcome (invitations.go) to
// the invite-result UI: `verb` shapes the headline (3c — email is the headline
// action), `note` is a non-empty amber line when the email did NOT go out (3a),
// and `linkIntro` frames the copyable link as a fallback ("Or copy…") on a
// successful send vs the primary handoff ("Share this link…") otherwise.
function inviteDelivery(outcome) {
  switch (outcome) {
    case 'sent':
      return {
        tone: 'sent',
        verb: 'emailed to',
        note: '',
        linkIntro: 'Or copy the link to share another way:',
      };
    case 'failed':
      return {
        tone: 'warn',
        verb: 'created for',
        note: 'The invite email couldn’t be delivered — share the link below directly.',
        linkIntro: 'Share this link with the invitee:',
      };
    case 'error':
      return {
        tone: 'warn',
        verb: 'created for',
        note: 'Something went wrong sending the email — share the link below directly.',
        linkIntro: 'Share this link with the invitee:',
      };
    case 'skipped_no_transport':
      return {
        tone: 'warn',
        verb: 'created for',
        note: 'Email isn’t configured for this organization, so nothing was sent — share the link below.',
        linkIntro: 'Share this link with the invitee:',
      };
    case 'skipped_no_public_host':
      return {
        tone: 'warn',
        verb: 'created for',
        note: 'Email was skipped because no public host is configured — share the link below.',
        linkIntro: 'Share this link with the invitee:',
      };
    default:
      // '' / omitted — no mailer wired (or the addMember fallback). Behaves
      // exactly like the pre-change UI: link is the primary handoff.
      return {
        tone: 'neutral',
        verb: 'created for',
        note: '',
        linkIntro: 'Share this link with the invitee:',
      };
  }
}

function provenanceBadge() {
  // Inline muted text — same color for every provenance. Like roleBadge,
  // these are categorical labels (how someone joined the org), not state;
  // the colored pill recipe was decorative. Mixed-case kept (some labels
  // are short phrases — "Invite accepted" — and would feel shouty in caps).
  return {
    fontSize: 11,
    fontWeight: 600,
    color: 'var(--color-text-mid)',
    letterSpacing: 0.2,
  };
}

function formatDate(s) {
  return formatFullDate(s) || '—';
}

function humanize(err, fallback) {
  if (!err) return fallback;
  if (err.status === 404) return 'User has not logged in to AxiaOps yet.';
  if (err.status === 409) return err.body || 'Conflict — last owner cannot be demoted; transfer ownership first.';
  if (err.status === 403) return 'You do not have permission for that change.';
  if (err.status === 400) return err.body || 'Invalid request.';
  return err.message || fallback;
}
