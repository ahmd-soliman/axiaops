import { useState } from 'react';
import { useTheme } from '../../theme/ThemeContext';
import { useApp } from '../../context/AppContext';
import { useToast } from '../../context/ToastContext';
import { useMe } from '../../context/MeContext';
import { deleteCurrentOrganization, patchOrganization } from '../../api/client';
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
  const { me, refresh } = useMe();
  const currentName = me?.organization?.name || orgName || '';

  return (
    <div style={{ padding: 24, color: t.textMid, maxWidth: 760 }}>
      <h1 style={{ margin: 0, color: t.text, fontSize: 22, fontWeight: 700 }}>Organization</h1>
      <p style={{ marginTop: 4, marginBottom: 24, color: t.textMuted, fontSize: 13 }}>
        Organization-level controls.
      </p>
      <RenameOrganizationSection t={t} isDark={isDark} currentName={currentName} toast={toast} refresh={refresh} />
      <DeleteOrganizationSection
        t={t}
        orgName={currentName}
        toast={toast}
        onLogout={onLogout}
      />
    </div>
  );
}

function RenameOrganizationSection({ t, isDark, currentName, toast, refresh }) {
  const [name, setName] = useState(currentName);
  const [saving, setSaving] = useState(false);
  const trimmed = name.trim();
  const dirty = trimmed !== currentName && trimmed.length > 0 && trimmed.length <= 120;

  async function onSubmit(e) {
    e.preventDefault();
    if (!dirty || saving) return;
    setSaving(true);
    try {
      await patchOrganization(trimmed);
      toast('Organization renamed.', 'success');
      await refresh();
    } catch (err) {
      const msg = err?.body?.message || 'Rename failed. Please retry.';
      toast(msg, 'error');
    } finally {
      setSaving(false);
    }
  }

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
      <h2 style={{ margin: 0, marginBottom: 6, fontSize: 14, fontWeight: 700, color: t.text }}>Organization Name</h2>
      <p style={{ marginTop: 0, marginBottom: 12, fontSize: 12, color: t.textMid, lineHeight: '18px' }}>
        Shown across the app and in invitation emails.
      </p>
      <form onSubmit={onSubmit} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={120}
          style={{
            flex: 1,
            padding: '7px 10px',
            border: `1px solid ${t.border}`,
            borderRadius: 6,
            backgroundColor: isDark ? 'rgba(0,0,0,0.2)' : '#fafafa',
            color: t.text,
            fontSize: 13,
          }}
        />
        <button
          type="submit"
          disabled={!dirty || saving}
          style={{
            padding: '7px 14px',
            border: 'none',
            borderRadius: 6,
            backgroundColor: t.accent,
            color: '#fff',
            fontWeight: 600,
            fontSize: 13,
            cursor: !dirty || saving ? 'not-allowed' : 'pointer',
            opacity: !dirty || saving ? 0.5 : 1,
          }}
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
      </form>
    </section>
  );
}

function DeleteOrganizationSection({ t, orgName, toast, onLogout }) {
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

function DangerSection({ t, title, blurb, buttonLabel, onClick, disabled, disabledHint, children }) {
  // Neutral card surface (t.border / t.surface) — same recipe as the benign
  // sections above and as Profile.jsx's DangerSection. Red title + red button
  // are the only chromatic cues; the colored border/tint earlier revisions
  // used was decorative redundancy. Pattern precedent: GitLab/Linear/Vercel
  // settings pages. Keep this in lockstep with Profile.jsx; extract to a
  // shared component if a third caller appears.
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
      <h2 style={{ margin: 0, marginBottom: 6, fontSize: 14, fontWeight: 700, color: t.error }}>{title}</h2>
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
          backgroundColor: t.error,
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
