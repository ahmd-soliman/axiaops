import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { useTheme } from '../theme/ThemeContext';
import { useMe } from '../context/MeContext';
import { useApp } from '../context/AppContext';
import { useToast } from '../context/ToastContext';
import { Overlay } from '../components/primitives';
import { downloadBlob } from '../utils/csv';
import {
  deleteCurrentTenant,
  deleteCurrentUser,
  exportTenantData,
} from '../api/client';
import { PERM } from '../api/permissions';

// Two destructive actions live behind a type-to-confirm modal so a stray
// click can't detonate the tenant; the export sits above so users can
// always grab a copy first. Success on either delete invalidates the
// session — we hand off to AppProvider.onLogout.

export default function Account() {
  const { theme: t, isDark } = useTheme();
  const { me, can } = useMe();
  const { orgName, onLogout } = useApp();
  const { toast } = useToast();

  return (
    <div style={{ padding: 24, color: t.textMid, maxWidth: 760 }}>
      <h1 style={{ margin: 0, color: t.text, fontSize: 22, fontWeight: 700 }}>Account</h1>
      <p style={{ marginTop: 4, marginBottom: 24, color: t.textMuted, fontSize: 13 }}>
        Your AxiaOps profile, data export, and erasure controls.
      </p>

      <ProfileSection t={t} me={me} orgName={orgName} />

      {can(PERM.DATA_EXPORT) && <ExportSection t={t} toast={toast} />}

      <DeleteUserSection
        t={t}
        isDark={isDark}
        email={me?.email}
        toast={toast}
        onLogout={onLogout}
      />

      {can(PERM.TENANT_DELETE) && (
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

function ProfileSection({ t, me, orgName }) {
  return (
    <Section t={t} title="Profile">
      <Field t={t} label="Email" value={me?.email || '—'} />
      <Field t={t} label="Role" value={me?.role || '—'} />
      <Field t={t} label="Tenant" value={orgName || me?.tenant_id || '—'} />
    </Section>
  );
}

function ExportSection({ t, toast }) {
  const [error, setError] = useState('');

  const mutation = useMutation({
    mutationFn: exportTenantData,
    onSuccess: ({ blob, filename }) => {
      downloadBlob(blob, filename);
      toast('Export downloaded.', 'success');
    },
    onError: (err) => setError(err.body || err.message || 'Failed to export data.'),
  });

  return (
    <Section t={t} title="Download my data">
      <p style={{ marginTop: 0, marginBottom: 12, fontSize: 12, color: t.textMid, lineHeight: '18px' }}>
        Generates a JSON file containing every per-tenant record we hold for this tenant — members,
        cloud accounts (without secrets), audit log, scan history, and detected resources. Satisfies
        GDPR Art. 15 (access) and Art. 20 (portability).
      </p>
      <button
        type="button"
        onClick={() => { setError(''); mutation.mutate(); }}
        disabled={mutation.isPending}
        style={primaryButton(t, mutation.isPending)}
      >
        {mutation.isPending ? 'Preparing…' : 'Download my data'}
      </button>
      {error && <Banner color="#fca5a5" bg="rgba(239,68,68,0.15)">{error}</Banner>}
    </Section>
  );
}

function DeleteUserSection({ t, isDark, email, toast, onLogout }) {
  const ctrl = useDestructiveConfirm({
    target: email || '',
    mutationFn: deleteCurrentUser,
    successMessage: 'Your account has been deleted.',
    onSuccess: onLogout,
    toast,
    on409: (err) =>
      err.body ||
      'You are the sole owner of one or more tenants. Transfer ownership in Users, or delete the tenant first.',
  });

  return (
    <DangerSection
      t={t}
      isDark={isDark}
      title="Delete my account"
      blurb="Permanently deletes your AxiaOps user. Your audit-log entries are anonymised across every tenant you belonged to. This cannot be undone."
      buttonLabel="Delete my account"
      onClick={ctrl.openModal}
      disabled={ctrl.target === ''}
      disabledHint="Email unavailable on your session — reload and try again."
    >
      <DestructiveConfirmModal
        ctrl={ctrl}
        title="Delete my account?"
        warning="This permanently deletes your user. You will be signed out and your audit-log entries across every tenant will be anonymised. This cannot be undone."
        targetLabel="email"
        confirmLabel="Delete account"
      />
    </DangerSection>
  );
}

function DeleteTenantSection({ t, isDark, orgName, toast, onLogout }) {
  const ctrl = useDestructiveConfirm({
    target: orgName || '',
    mutationFn: deleteCurrentTenant,
    successMessage: 'Tenant deleted.',
    onSuccess: onLogout,
    toast,
  });

  return (
    <DangerSection
      t={t}
      isDark={isDark}
      title="Delete this tenant"
      blurb="Permanently deletes the entire tenant and every record it owns: cloud accounts, scan history, dismissals, audit log, and member memberships. Members lose access immediately. This cannot be undone."
      buttonLabel="Delete tenant"
      onClick={ctrl.openModal}
      disabled={ctrl.target === ''}
      disabledHint="Tenant name unavailable on your session — reload and try again."
    >
      <DestructiveConfirmModal
        ctrl={ctrl}
        title="Delete this tenant?"
        warning={`This permanently wipes everything for tenant ${orgName || '—'}: accounts, resources, costs, snapshots, dismissals, audit log, and all memberships. Every member is signed out. This cannot be undone.`}
        targetLabel="tenant name"
        confirmLabel="Delete tenant"
      />
    </DangerSection>
  );
}

// useDestructiveConfirm owns the type-to-confirm state machine: open/close,
// what was typed, comparison against the target string, mutation lifecycle,
// and the special-case 409 handler (sole-owner refusal on /users/me).
function useDestructiveConfirm({ target, mutationFn, successMessage, onSuccess, toast, on409 }) {
  const [open, setOpen] = useState(false);
  const [confirmText, setConfirmText] = useState('');
  const [error, setError] = useState('');

  const mutation = useMutation({
    mutationFn,
    onSuccess: () => {
      toast(successMessage, 'success');
      onSuccess?.();
    },
    onError: (err) => {
      if (err.status === 409 && on409) {
        setError(on409(err));
        return;
      }
      setError(err.body || err.message || 'Action failed.');
    },
  });

  return {
    target,
    open,
    openModal: () => setOpen(true),
    close: () => { setOpen(false); setConfirmText(''); setError(''); },
    confirmText,
    setConfirmText,
    matches: target !== '' && confirmText === target,
    error,
    isPending: mutation.isPending,
    confirm: () => { setError(''); mutation.mutate(); },
  };
}

function DestructiveConfirmModal({ ctrl, title, warning, targetLabel, confirmLabel }) {
  const { theme: t, isDark } = useTheme();
  const targetMissing = ctrl.target === '';

  return (
    <Overlay visible={ctrl.open} onClose={ctrl.close}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
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
        <h3 style={{ margin: 0, marginBottom: 12, fontSize: 16, fontWeight: 700, color: t.text }}>{title}</h3>
        <p style={modalText(t)}>{warning}</p>
        {targetMissing ? (
          <Banner color="#fbbf24" bg="rgba(251,191,36,0.15)">
            {targetLabel} is unavailable; cannot proceed safely. Reload and try again.
          </Banner>
        ) : (
          <>
            <p style={modalText(t)}>
              Type the {targetLabel} <strong style={{ color: t.text }}>{ctrl.target}</strong> to confirm:
            </p>
            <input
              type="text"
              autoFocus
              value={ctrl.confirmText}
              onChange={(e) => ctrl.setConfirmText(e.target.value)}
              placeholder={ctrl.target}
              style={{ ...inputStyle(t), width: '100%', boxSizing: 'border-box' }}
            />
          </>
        )}
        {ctrl.error && <Banner color="#fca5a5" bg="rgba(239,68,68,0.15)">{ctrl.error}</Banner>}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 16 }}>
          <button type="button" onClick={ctrl.close} style={ghostButton(t)}>Cancel</button>
          <button
            type="button"
            onClick={ctrl.confirm}
            disabled={!ctrl.matches || ctrl.isPending}
            style={primaryDangerButton(!ctrl.matches || ctrl.isPending)}
          >
            {ctrl.isPending ? 'Working…' : confirmLabel}
          </button>
        </div>
      </div>
    </Overlay>
  );
}

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
      <h2 style={{ margin: 0, marginBottom: 12, fontSize: 14, fontWeight: 700, color: t.text }}>{title}</h2>
      {children}
    </section>
  );
}

function DangerSection({ t, isDark, title, blurb, buttonLabel, onClick, disabled, disabledHint, children }) {
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
      <h2 style={{ margin: 0, marginBottom: 6, fontSize: 14, fontWeight: 700, color: '#ef4444' }}>{title}</h2>
      <p style={{ marginTop: 0, marginBottom: 12, fontSize: 12, color: t.textMid, lineHeight: '18px' }}>{blurb}</p>
      <button
        type="button"
        onClick={onClick}
        disabled={disabled}
        title={disabled ? disabledHint : undefined}
        style={primaryDangerButton(disabled)}
      >
        {buttonLabel}
      </button>
      {disabled && disabledHint && (
        <p style={{ marginTop: 8, marginBottom: 0, fontSize: 12, color: t.textMuted }}>{disabledHint}</p>
      )}
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

function primaryButton(t, disabled) {
  return {
    padding: '7px 14px',
    border: 'none',
    borderRadius: 6,
    backgroundColor: t.accent,
    color: '#fff',
    fontWeight: 600,
    fontSize: 13,
    cursor: disabled ? 'not-allowed' : 'pointer',
    opacity: disabled ? 0.55 : 1,
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
