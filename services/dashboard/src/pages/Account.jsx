import { useTheme } from '../theme/ThemeContext';
import { useMe } from '../context/MeContext';
import { useApp } from '../context/AppContext';
import { useToast } from '../context/ToastContext';
import { deleteCurrentTenant } from '../api/client';
import { PERM } from '../api/permissions';
import {
  useDestructiveConfirm,
  DestructiveConfirmModal,
} from '../components/DestructiveConfirm';

// Transitional shell after the personal sections moved to /profile. The
// tenant-delete control will move to /settings/workspace in the next
// commit and this file deletes. Kept here so /account doesn't 404 mid-PR.
export default function Account() {
  const { theme: t, isDark } = useTheme();
  const { can } = useMe();
  const { orgName, onLogout } = useApp();
  const { toast } = useToast();

  if (!can(PERM.TENANT_DELETE)) {
    return (
      <div style={{ padding: 24, color: t.textMid }}>
        <p style={{ fontSize: 13 }}>Nothing to manage here. See <a href="/profile" style={{ color: t.accent }}>My profile</a>.</p>
      </div>
    );
  }

  return (
    <div style={{ padding: 24, color: t.textMid, maxWidth: 760 }}>
      <h1 style={{ margin: 0, color: t.text, fontSize: 22, fontWeight: 700 }}>Workspace</h1>
      <p style={{ marginTop: 4, marginBottom: 24, color: t.textMuted, fontSize: 13 }}>
        Tenant-level destructive controls.
      </p>
      <DeleteTenantSection
        t={t}
        isDark={isDark}
        orgName={orgName}
        toast={toast}
        onLogout={onLogout}
      />
    </div>
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
        style={{
          padding: '7px 14px',
          border: 'none',
          borderRadius: 6,
          backgroundColor: '#ef4444',
          color: '#fff',
          fontWeight: 600,
          fontSize: 13,
          cursor: disabled ? 'not-allowed' : 'pointer',
          opacity: disabled ? 0.55 : 1,
        }}
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
