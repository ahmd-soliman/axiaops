import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { useMe } from '../context/MeContext';
import { useApp } from '../context/AppContext';
import { useToast } from '../context/ToastContext';
import { downloadBlob } from '../utils/csv';
import { deleteCurrentUser, exportOrganizationData } from '../api/client';
import { PERM } from '../api/permissions';
import {
  useDestructiveConfirm,
  DestructiveConfirmModal,
} from '../components/DestructiveConfirm';
import { DangerSection } from '../components/DangerSection';

// "Manage me" surface: read-only profile + GDPR Art. 15/20 export +
// Art. 17 self-erasure. Organization-level destructive actions (delete
// organization, transfer ownership) live under /settings/organization —
// they're org admin, not personal.
export default function Profile() {
  const { me, can } = useMe();
  const { orgName, onLogout } = useApp();
  const { toast } = useToast();

  return (
    <div style={{ padding: 24, color: 'var(--color-text-mid)', maxWidth: 760 }}>
      <h1 style={{ margin: 0, color: 'var(--color-text)', fontSize: 22, fontWeight: 700 }}>My Profile</h1>
      <p style={{ marginTop: 4, marginBottom: 24, color: 'var(--color-text-muted)', fontSize: 13 }}>
        Your AxiaOps account, data export, and self-erasure controls.
      </p>

      <Section title="Profile">
        <Field label="Display name" value={me?.name || '—'} />
        <Field label="Email" value={me?.email || '—'} />
        <Field label="Role" value={me?.role || '—'} />
        <Field label="Organization" value={me?.organization?.name || orgName || me?.organization_id || '—'} />
      </Section>

      {can(PERM.DATA_EXPORT) && <ExportSection toast={toast} />}

      <DeleteUserSection
        email={me?.email}
        toast={toast}
        onLogout={onLogout}
      />
    </div>
  );
}

function ExportSection({ toast }) {
  const [error, setError] = useState('');

  const mutation = useMutation({
    mutationFn: exportOrganizationData,
    onSuccess: ({ blob, filename }) => {
      downloadBlob(blob, filename);
      toast('Export downloaded.', 'success');
    },
    onError: (err) => setError(err.body || err.message || 'Failed to export data.'),
  });

  return (
    <Section title="Download My Data">
      <p style={{ marginTop: 0, marginBottom: 12, fontSize: 12, color: 'var(--color-text-mid)', lineHeight: '18px' }}>
        Generates a JSON file containing every record AxiaOps holds for your organization — members,
        cloud accounts (without secrets), audit log, scan history, and detected resources. Satisfies
        GDPR Art. 15 (access) and Art. 20 (portability).
      </p>
      <button
        type="button"
        onClick={() => { setError(''); mutation.mutate(); }}
        disabled={mutation.isPending}
        style={primaryButton(mutation.isPending)}
      >
        {mutation.isPending ? 'Preparing…' : 'Download My Data'}
      </button>
      {error && <InlineBanner color={'var(--color-error)'} bg="rgba(239,68,68,0.15)">{error}</InlineBanner>}
    </Section>
  );
}

function DeleteUserSection({ email, toast, onLogout }) {
  const ctrl = useDestructiveConfirm({
    target: email || '',
    mutationFn: deleteCurrentUser,
    successMessage: 'Your account has been deleted.',
    onSuccess: onLogout,
    toast,
    on409: (err) =>
      err.body ||
      'You are the sole owner of one or more organizations. Transfer ownership in Settings → Team, or delete the organization from Settings → Organization.',
  });

  return (
    <DangerSection
      title="Delete My Account"
      blurb="Permanently deletes your AxiaOps user. Your audit-log entries are anonymised across your organizations. This cannot be undone."
      buttonLabel="Delete My Account"
      onClick={ctrl.openModal}
      disabled={ctrl.target === ''}
      disabledHint="Email unavailable on your session — reload and try again."
    >
      <DestructiveConfirmModal
        ctrl={ctrl}
        title="Delete My Account?"
        warning="This permanently deletes your user. You will be signed out and your audit-log entries across your organizations will be anonymised. This cannot be undone."
        targetLabel="email"
        confirmLabel="Delete Account"
      />
    </DangerSection>
  );
}

function Section({ title, children }) {
  return (
    <section
      style={{
        border: `1px solid var(--color-border)`,
        borderRadius: 8,
        padding: 16,
        marginBottom: 16,
        backgroundColor: 'var(--color-surface)',
      }}
    >
      <h2 style={{ margin: 0, marginBottom: 12, fontSize: 14, fontWeight: 700, color: 'var(--color-text)' }}>{title}</h2>
      {children}
    </section>
  );
}

function Field({ label, value }) {
  return (
    <div style={{ display: 'flex', gap: 12, padding: '6px 0', fontSize: 13 }}>
      <div style={{ width: 96, color: 'var(--color-text-muted)' }}>{label}</div>
      <div style={{ color: 'var(--color-text)', fontWeight: 500, wordBreak: 'break-all' }}>{value}</div>
    </div>
  );
}

function InlineBanner({ children, color, bg }) {
  return (
    <div style={{ padding: '8px 12px', marginTop: 12, borderRadius: 6, color, backgroundColor: bg, fontSize: 12 }}>
      {children}
    </div>
  );
}

function primaryButton(disabled) {
  return {
    padding: '7px 14px',
    border: 'none',
    borderRadius: 6,
    backgroundColor: 'var(--color-accent)',
    color: 'var(--color-text-on-dark)',
    fontWeight: 600,
    fontSize: 13,
    cursor: disabled ? 'not-allowed' : 'pointer',
    opacity: disabled ? 0.55 : 1,
  };
}

