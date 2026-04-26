import { useTheme } from '../../theme/ThemeContext';
import { useApp } from '../../context/AppContext';
import { useToast } from '../../context/ToastContext';
import { deleteCurrentOrganization } from '../../api/client';
import {
  useDestructiveConfirm,
  DestructiveConfirmModal,
} from '../../components/DestructiveConfirm';

// Organization tab — organization-level destructive controls. Only owners
// reach this route (Settings sub-nav filters on PERM.ORGANIZATION_DELETE
// before rendering the tab), so no extra in-page perm check is needed.
//
// Future home for transfer-ownership UI, notification preferences,
// billing controls, and the org display-name editor.
export default function Organization() {
  const { theme: t, isDark } = useTheme();
  const { orgName, onLogout } = useApp();
  const { toast } = useToast();

  return (
    <div style={{ padding: 24, color: t.textMid, maxWidth: 760 }}>
      <h1 style={{ margin: 0, color: t.text, fontSize: 22, fontWeight: 700 }}>Organization</h1>
      <p style={{ marginTop: 4, marginBottom: 24, color: t.textMuted, fontSize: 13 }}>
        Organization-level controls.
      </p>
      <DeleteOrganizationSection
        t={t}
        isDark={isDark}
        orgName={orgName}
        toast={toast}
        onLogout={onLogout}
      />
    </div>
  );
}

function DeleteOrganizationSection({ t, isDark, orgName, toast, onLogout }) {
  const ctrl = useDestructiveConfirm({
    target: orgName || '',
    mutationFn: deleteCurrentOrganization,
    successMessage: 'Organization deleted.',
    onSuccess: onLogout,
    toast,
  });

  return (
    <DangerSection
      t={t}
      isDark={isDark}
      title="Delete This Organization"
      blurb="Permanently deletes the entire organization and every record it owns: cloud accounts, scan history, dismissals, audit log, and member memberships. Members lose access immediately. This cannot be undone."
      buttonLabel="Delete Organization"
      onClick={ctrl.openModal}
      disabled={ctrl.target === ''}
      disabledHint="Organization name unavailable on your session — reload and try again."
    >
      <DestructiveConfirmModal
        ctrl={ctrl}
        title="Delete This Organization?"
        warning={`This permanently wipes everything for ${orgName || '—'}: accounts, resources, costs, snapshots, dismissals, audit log, and all memberships. Every member is signed out. This cannot be undone.`}
        targetLabel="organization name"
        confirmLabel="Delete Organization"
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
