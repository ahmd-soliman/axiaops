import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTheme } from '../../theme/ThemeContext';
import { useMe } from '../../context/MeContext';
import { useToast } from '../../context/ToastContext';
import { patchOrganization } from '../../api/client';

// Step 1 of 3 — confirm/rename the organization. Pre-fills with the Kinde
// default (me.organization.name). Not skippable; on submit, advances to step 2.
// See docs/onboarding-wizard.md §8.3.
export default function OnboardingOrgName() {
  const { theme: t, isDark } = useTheme();
  const navigate = useNavigate();
  const { me, refresh } = useMe();
  const { toast } = useToast();
  const [name, setName] = useState(me?.organization?.name || '');
  const [saving, setSaving] = useState(false);

  const trimmed = name.trim();
  const valid = trimmed.length > 0 && trimmed.length <= 120;
  const border = isDark ? 'rgba(255,255,255,0.12)' : '#e5e7eb';

  async function onSubmit(e) {
    e.preventDefault();
    if (!valid || saving) return;
    setSaving(true);
    try {
      await patchOrganization(trimmed);
      // Navigate before refresh — refreshing first can trigger MeContext state
      // updates that re-evaluate OnboardingGate while we're still on this
      // route, briefly remounting before navigation lands. Navigate first so
      // the route transition happens cleanly, then refresh in the background.
      navigate('/onboarding/invite', { replace: true });
      refresh();
    } catch (err) {
      const msg = err?.body?.message || 'Could not save the name. Please retry.';
      toast(msg, 'error');
      setSaving(false);
    }
  }

  return (
    <form onSubmit={onSubmit}>
      <h1 style={{ color: t.text, fontSize: 26, fontWeight: 700, margin: 0, marginBottom: 8 }}>
        Welcome to AxiaOps
      </h1>
      <p style={{ color: t.textMid, fontSize: 14, marginTop: 0, marginBottom: 24 }}>
        Let&apos;s start by confirming your organization name. This appears on
        invitation emails and across the app.
      </p>
      <label
        style={{
          display: 'block',
          color: t.textMuted,
          fontSize: 12,
          fontWeight: 600,
          marginBottom: 6,
          textTransform: 'uppercase',
          letterSpacing: 0.5,
        }}
      >
        Organization Name
      </label>
      <input
        autoFocus
        type="text"
        value={name}
        onChange={(e) => setName(e.target.value)}
        maxLength={120}
        placeholder="Acme Corp"
        style={{
          width: '100%',
          padding: '10px 12px',
          border: `1px solid ${border}`,
          borderRadius: 8,
          backgroundColor: isDark ? 'rgba(0,0,0,0.2)' : '#fff',
          color: t.text,
          fontSize: 15,
          marginBottom: 24,
          boxSizing: 'border-box',
        }}
      />
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button
          type="submit"
          disabled={!valid || saving}
          style={{
            padding: '10px 20px',
            border: 'none',
            borderRadius: 8,
            backgroundColor: t.accent,
            color: '#fff',
            fontWeight: 600,
            fontSize: 14,
            cursor: !valid || saving ? 'not-allowed' : 'pointer',
            opacity: !valid || saving ? 0.5 : 1,
          }}
        >
          {saving ? 'Saving…' : 'Continue'}
        </button>
      </div>
    </form>
  );
}
