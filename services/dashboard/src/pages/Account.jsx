import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { useTheme } from '../theme/ThemeContext';
import { useMe } from '../context/MeContext';
import { useApp } from '../context/AppContext';
import { useToast } from '../context/ToastContext';
import { deleteCurrentTenant, deleteCurrentUser } from '../api/client';

// GDPR right-to-erasure surface. Two destructive actions, each behind a
// type-to-confirm modal:
//
//   - Delete my account (any logged-in user) → DELETE /v1/users/me
//   - Delete this tenant (owners only)       → DELETE /v1/tenants/me
//
// On success either action invalidates the session entirely (the user or
// their tenant is gone), so we hand off to AppProvider.onLogout which clears
// the token + query cache and routes to /login.
//
// The 409 case on /users/me — "you are the sole owner of one or more tenants"
// — is handled inline so the user is told to use the Users page (transfer
// ownership) or to delete the tenant first, instead of being silently blocked.

export default function Account() {
  const { theme: t, isDark } = useTheme();
  const { me, can } = useMe();
  const { orgName, onLogout } = useApp();
  const { toast } = useToast();

  const canDeleteTenant = can('tenant:delete');

  return (
    <div style={{ padding: 24, color: t.textMid, maxWidth: 760 }}>
      <h1 style={{ margin: 0, color: t.text, fontSize: 22, fontWeight: 700 }}>Account</h1>
      <p style={{ marginTop: 4, marginBottom: 24, color: t.textMuted, fontSize: 13 }}>
        Your AxiaOps profile and data-erasure controls.
      </p>

      <ProfileSection t={t} me={me} orgName={orgName} />

      <DeleteUserSection
        t={t}
        isDark={isDark}
        email={me?.email}
        toast={toast}
        onLogout={onLogout}
      />

      {canDeleteTenant && (
        <DeleteTenantSection
          t={t}
          isDark={isDark}
          orgName={orgName}
          toast={toast}
          onLogout={onLogout}
        />
      )}
    </div>
  );
}

// ── Profile (read-only for now) ─────────────────────────────────────────────

function ProfileSection({ t, me, orgName }) {
  return (
    <Section t={t} title="Profile">
      <Field t={t} label="Email" value={me?.email || '—'} />
      <Field t={t} label="Role" value={me?.role || '—'} />
      <Field t={t} label="Tenant" value={orgName || me?.tenant_id || '—'} />
    </Section>
  );
}

// ── Danger zone: delete my user ─────────────────────────────────────────────

function DeleteUserSection({ t, isDark, email, toast, onLogout }) {
  const [open, setOpen] = useState(false);
  const [confirmText, setConfirmText] = useState('');
  const [error, setError] = useState('');

  const target = email || '';
  const matches = target !== '' && confirmText === target;

  const mutation = useMutation({
    mutationFn: deleteCurrentUser,
    onSuccess: () => {
      toast('Your account has been deleted.', 'success');
      onLogout();
    },
    onError: (err) => {
      if (err.status === 409) {
        setError(
          err.body ||
            'You are the sole owner of one or more tenants. Transfer ownership in Users, or delete the tenant first.',
        );
        return;
      }
      setError(err.body || err.message || 'Failed to delete account.');
    },
  });

  const cancel = () => {
    setOpen(false);
    setConfirmText('');
    setError('');
  };

  return (
    <DangerSection
      t={t}
      isDark={isDark}
      title="Delete my account"
      blurb="Permanently deletes your AxiaOps user. Your audit-log entries are anonymised across every tenant you belonged to. This cannot be undone."
      buttonLabel="Delete my account"
      onClick={() => setOpen(true)}
    >
      {open && (
        <ConfirmModal
          t={t}
          isDark={isDark}
          title="Delete my account?"
          body={
            <>
              <p style={modalText(t)}>
                This permanently deletes your user. You will be signed out and your audit-log
                entries across every tenant will be anonymised. This cannot be undone.
              </p>
              <p style={modalText(t)}>
                Type your email <strong style={{ color: t.text }}>{target}</strong> to confirm:
              </p>
              <input
                type="text"
                autoFocus
                value={confirmText}
                onChange={(e) => setConfirmText(e.target.value)}
                placeholder={target}
                style={{ ...inputStyle(t), width: '100%', boxSizing: 'border-box' }}
              />
              {error && <Banner color="#fca5a5" bg="rgba(239,68,68,0.15)">{error}</Banner>}
            </>
          }
          confirmLabel={mutation.isPending ? 'Deleting…' : 'Delete account'}
          confirmDisabled={!matches || mutation.isPending}
          onConfirm={() => {
            setError('');
            mutation.mutate();
          }}
          onCancel={cancel}
        />
      )}
    </DangerSection>
  );
}

// ── Danger zone: delete tenant ──────────────────────────────────────────────

function DeleteTenantSection({ t, isDark, orgName, toast, onLogout }) {
  const [open, setOpen] = useState(false);
  const [confirmText, setConfirmText] = useState('');
  const [error, setError] = useState('');

  const target = orgName || '';
  const matches = target !== '' && confirmText === target;

  const mutation = useMutation({
    mutationFn: deleteCurrentTenant,
    onSuccess: () => {
      toast('Tenant deleted.', 'success');
      onLogout();
    },
    onError: (err) => {
      setError(err.body || err.message || 'Failed to delete tenant.');
    },
  });

  const cancel = () => {
    setOpen(false);
    setConfirmText('');
    setError('');
  };

  return (
    <DangerSection
      t={t}
      isDark={isDark}
      title="Delete this tenant"
      blurb="Permanently deletes the entire tenant and every record it owns: cloud accounts, scan history, dismissals, audit log, and member memberships. Members lose access immediately. This cannot be undone."
      buttonLabel="Delete tenant"
      onClick={() => setOpen(true)}
    >
      {open && (
        <ConfirmModal
          t={t}
          isDark={isDark}
          title="Delete this tenant?"
          body={
            <>
              <p style={modalText(t)}>
                This permanently wipes everything for tenant <strong style={{ color: t.text }}>{target || '—'}</strong>:
                accounts, resources, costs, snapshots, dismissals, audit log, and all memberships.
                Every member is signed out. This cannot be undone.
              </p>
              {target ? (
                <>
                  <p style={modalText(t)}>
                    Type the tenant name <strong style={{ color: t.text }}>{target}</strong> to confirm:
                  </p>
                  <input
                    type="text"
                    autoFocus
                    value={confirmText}
                    onChange={(e) => setConfirmText(e.target.value)}
                    placeholder={target}
                    style={{ ...inputStyle(t), width: '100%', boxSizing: 'border-box' }}
                  />
                </>
              ) : (
                <Banner color="#fbbf24" bg="rgba(251,191,36,0.15)">
                  Tenant name is unavailable; cannot proceed safely. Reload and try again.
                </Banner>
              )}
              {error && <Banner color="#fca5a5" bg="rgba(239,68,68,0.15)">{error}</Banner>}
            </>
          }
          confirmLabel={mutation.isPending ? 'Deleting…' : 'Delete tenant'}
          confirmDisabled={!matches || mutation.isPending}
          onConfirm={() => {
            setError('');
            mutation.mutate();
          }}
          onCancel={cancel}
        />
      )}
    </DangerSection>
  );
}

// ── Visual primitives ───────────────────────────────────────────────────────

function Section({ t, title, children }) {
  return (
    <section
      style={{
        border: `1px solid ${t.border}`,
        borderRadius: 8,
        padding: 16,
        marginBottom: 16,
        backgroundColor: t.surface,
      }}
    >
      <h2 style={{ margin: 0, marginBottom: 12, fontSize: 14, fontWeight: 700, color: t.text }}>
        {title}
      </h2>
      {children}
    </section>
  );
}

function DangerSection({ t, isDark, title, blurb, buttonLabel, onClick, children }) {
  const dangerBorder = isDark ? 'rgba(239,68,68,0.5)' : '#fecaca';
  const dangerTint = isDark ? 'rgba(239,68,68,0.07)' : '#fef2f2';
  return (
    <section
      style={{
        border: `1px solid ${dangerBorder}`,
        borderRadius: 8,
        padding: 16,
        marginBottom: 16,
        backgroundColor: dangerTint,
      }}
    >
      <h2 style={{ margin: 0, marginBottom: 6, fontSize: 14, fontWeight: 700, color: '#ef4444' }}>
        {title}
      </h2>
      <p style={{ marginTop: 0, marginBottom: 12, fontSize: 12, color: t.textMid, lineHeight: '18px' }}>
        {blurb}
      </p>
      <button type="button" onClick={onClick} style={primaryDangerButton()}>
        {buttonLabel}
      </button>
      {children}
    </section>
  );
}

function Field({ t, label, value }) {
  return (
    <div style={{ display: 'flex', gap: 12, padding: '6px 0', fontSize: 13 }}>
      <div style={{ width: 96, color: t.textMuted }}>{label}</div>
      <div style={{ color: t.text, fontWeight: 500, wordBreak: 'break-all' }}>{value}</div>
    </div>
  );
}

function ConfirmModal({ t, isDark, title, body, confirmLabel, confirmDisabled, onConfirm, onCancel }) {
  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={title}
      onClick={onCancel}
      style={{
        position: 'fixed',
        inset: 0,
        backgroundColor: 'rgba(0,0,0,0.5)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 200,
        padding: 16,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          backgroundColor: t.surface,
          border: `1px solid ${isDark ? 'rgba(239,68,68,0.5)' : '#fecaca'}`,
          borderRadius: 10,
          padding: 20,
          maxWidth: 480,
          width: '100%',
          boxShadow: '0 12px 32px rgba(0,0,0,0.3)',
        }}
      >
        <h3 style={{ margin: 0, marginBottom: 12, fontSize: 16, fontWeight: 700, color: t.text }}>
          {title}
        </h3>
        {body}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 16 }}>
          <button type="button" onClick={onCancel} style={ghostButton(t)}>
            Cancel
          </button>
          <button type="button" onClick={onConfirm} disabled={confirmDisabled} style={primaryDangerButton(confirmDisabled)}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

function Banner({ children, color, bg }) {
  return (
    <div style={{ padding: '8px 12px', marginTop: 12, borderRadius: 6, color, backgroundColor: bg, fontSize: 12 }}>
      {children}
    </div>
  );
}

function modalText(t) {
  return { margin: '0 0 10px 0', fontSize: 13, color: t.textMid, lineHeight: '20px' };
}

function inputStyle(t) {
  return {
    padding: '8px 10px',
    border: `1px solid ${t.border}`,
    borderRadius: 6,
    fontSize: 13,
    backgroundColor: t.bg,
    color: t.text,
  };
}

function ghostButton(t) {
  return {
    padding: '7px 14px',
    border: `1px solid ${t.border}`,
    borderRadius: 6,
    backgroundColor: 'transparent',
    color: t.text,
    fontWeight: 600,
    fontSize: 13,
    cursor: 'pointer',
  };
}

function primaryDangerButton(disabled) {
  return {
    padding: '7px 14px',
    border: 'none',
    borderRadius: 6,
    backgroundColor: '#ef4444',
    color: '#fff',
    fontWeight: 600,
    fontSize: 13,
    cursor: disabled ? 'not-allowed' : 'pointer',
    opacity: disabled ? 0.55 : 1,
  };
}
