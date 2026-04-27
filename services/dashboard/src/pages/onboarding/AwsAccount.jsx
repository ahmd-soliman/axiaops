import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTheme } from '../../theme/ThemeContext';
import { useToast } from '../../context/ToastContext';
import { completeOnboarding } from '../../api/client';

// Step 3 of 3 — connect first AWS account. Skippable. Whether the user
// connects or skips, completeOnboarding marks the wizard done so it never
// re-triggers. See docs/onboarding-wizard.md §8.3.
//
// To keep this PR shippable without depending on a refactor of pages/Connect.jsx,
// we link to the existing /connect screen rather than embedding the form.
// A future cleanup can extract a shared <ConnectAccountForm> component.
export default function OnboardingAwsAccount() {
  const { theme: t, isDark } = useTheme();
  const navigate = useNavigate();
  const { toast } = useToast();
  const [finishing, setFinishing] = useState(false);

  const border = isDark ? 'rgba(255,255,255,0.12)' : '#e5e7eb';

  async function finish(stepsSkipped) {
    if (finishing) return;
    setFinishing(true);
    try {
      await completeOnboarding(stepsSkipped);
    } catch (err) {
      // Soft-fail — completion is idempotent and the gate will re-route here
      // if the flag didn't flip. Toast and proceed.
      toast({ kind: 'error', message: 'Could not save onboarding state. You may see the wizard again.' });
    }
    navigate('/');
  }

  function goConnect() {
    // The connect screen will live at /connect post-onboarding. Mark
    // onboarding done first (only invite was potentially skipped), then
    // forward there. Connect's own form posts to /v1/accounts.
    finish([]).then(() => navigate('/connect'));
  }

  function skip() {
    finish(['aws-account']);
  }

  return (
    <div>
      <h1 style={{ color: t.text, fontSize: 26, fontWeight: 700, margin: 0, marginBottom: 8 }}>
        Connect your first AWS account
      </h1>
      <p style={{ color: t.textMid, fontSize: 14, marginTop: 0, marginBottom: 24 }}>
        AxiaOps reads CloudWatch and Cost Explorer with read-only credentials
        to detect idle resources. You can do this now or later from
        the dashboard.
      </p>

      <section
        style={{
          border: `1px solid ${border}`,
          borderRadius: 8,
          padding: 16,
          backgroundColor: isDark ? 'rgba(255,255,255,0.03)' : '#fff',
          marginBottom: 24,
        }}
      >
        <h2 style={{ margin: 0, marginBottom: 8, fontSize: 14, fontWeight: 700, color: t.text }}>
          What we&apos;ll need
        </h2>
        <ul style={{ margin: 0, paddingLeft: 20, color: t.textMid, fontSize: 13, lineHeight: '20px' }}>
          <li>An AWS access key + secret with the AxiaOps IAM policy attached</li>
          <li>The AWS account ID and a region (we recommend the one with most activity)</li>
        </ul>
      </section>

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <button
          type="button"
          onClick={skip}
          disabled={finishing}
          style={{
            padding: '10px 16px',
            border: 'none',
            borderRadius: 8,
            backgroundColor: 'transparent',
            color: t.textMuted,
            fontSize: 14,
            cursor: 'pointer',
          }}
        >
          Skip and finish
        </button>
        <button
          type="button"
          onClick={goConnect}
          disabled={finishing}
          style={{
            padding: '10px 20px',
            border: 'none',
            borderRadius: 8,
            backgroundColor: t.accent,
            color: '#fff',
            fontWeight: 600,
            fontSize: 14,
            cursor: finishing ? 'not-allowed' : 'pointer',
            opacity: finishing ? 0.5 : 1,
          }}
        >
          {finishing ? 'Finishing…' : 'Connect AWS account'}
        </button>
      </div>
    </div>
  );
}
