import { useState } from 'react';
import { serviceConfig } from '../components/serviceConfig';
import { useTheme } from '../theme/ThemeContext';
import { useToast } from '../context/ToastContext';
import { dismissZombie, fetchDismissals, revokeDismissal } from '../api/client';
import { Overlay, Spinner } from '../components/primitives';
import { formatDate } from '../utils/formatDate';

const DISMISS_REASONS = [
  { value: 'intentional',        label: 'Intentionally idle' },
  { value: 'scheduled_deletion', label: 'Scheduled for deletion' },
  { value: 'false_positive',     label: 'False positive' },
  { value: 'cost_accepted',      label: 'Cost accepted' },
  { value: 'other',              label: 'Other (add note)' },
];

const SNOOZE_OPTIONS = [
  { label: '1 day',  days: 1 },
  { label: '7 days', days: 7 },
  { label: '30 days', days: 30 },
  { label: '90 days', days: 90 },
];

const METRIC_LABELS = {
  CPUUtilization:          'CPU Usage',
  DatabaseConnections:     'DB Connections',
  Invocations:             'Invocations',
  RequestCount:            'Requests',
  BytesOutToDestination:   'Data Transfer',
  NetworkInterfaceAttachment: 'Attachment',
};

function fmtDate(iso) {
  return formatDate(iso);
}

function remediationCommand(service, resourceId = '', region = 'eu-central-1') {
  const r = `--region ${region}`;
  if (service === 'AmazonVPC') {
    if (resourceId.startsWith('eipalloc-')) return `aws ec2 release-address --allocation-id ${resourceId} ${r}`;
    return `aws ec2 delete-nat-gateway --nat-gateway-id ${resourceId} ${r}`;
  }
  const cmds = {
    AmazonEC2:                  `aws ec2 stop-instances --instance-ids ${resourceId} ${r}`,
    AmazonRDS:                  `aws rds delete-db-instance --db-instance-identifier ${resourceId} --skip-final-snapshot ${r}`,
    AWSLambda:                  `aws lambda delete-function --function-name ${resourceId} ${r}`,
    AmazonElasticLoadBalancing: `aws elbv2 delete-load-balancer --load-balancer-arn ${resourceId} ${r}`,
  };
  return cmds[service] ?? null;
}

function remediationHint(service, resourceId = '') {
  if (service === 'AmazonVPC') {
    if (resourceId.startsWith('eipalloc-')) return 'Release this Elastic IP in the EC2 console under Network & Security → Elastic IPs. This stops the $0.005/hour idle charge immediately.';
    return 'Delete the NAT Gateway. Once deleted, release any associated Elastic IP to stop charges.';
  }
  const hints = {
    AmazonEC2:                  'Stop or terminate the instance. If it is part of an Auto Scaling group, remove it from the group first.',
    AmazonRDS:                  'Create a final snapshot, then delete the DB instance. Confirm with the owner before deleting.',
    AWSLambda:                  'Delete the function. Check for any EventBridge rules or triggers pointing to it first.',
    AmazonElasticLoadBalancing: 'Delete the load balancer. Verify no DNS records point to it.',
  };
  return hints[service] ?? 'Review with the resource owner before taking action.';
}

function CLICommand({ cmd }) {
  const [copied, setCopied] = useState(false);
  if (!cmd) return null;

  function handleCopy() {
    navigator?.clipboard?.writeText(cmd).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }

  return (
    <div style={{ marginTop: 10, borderRadius: 10, overflow: 'hidden', border: `1px solid var(--color-border)` }}>
      <div style={{ backgroundColor: 'var(--color-bg-secondary)', padding: '12px 14px', position: 'relative' }}>
        <code style={{ fontFamily: '"Geist Mono Variable", monospace', fontSize: 12, color: 'var(--color-text)', lineHeight: '20px', display: 'block', paddingRight: 36, wordBreak: 'break-all' }}>
          {cmd}
        </code>
        <button
          onClick={handleCopy}
          aria-label={copied ? 'Copied!' : 'Copy command'}
          style={{ position: 'absolute', top: 10, right: 10, padding: 6, background: 'none', border: 'none', cursor: 'pointer', fontSize: 14, color: copied ? 'var(--color-success)' : 'var(--color-text-muted)' }}
        >
          {copied ? '✓' : '⧉'}
        </button>
      </div>
    </div>
  );
}

function SectionLabel({ children }) {
  return (
    <span style={{ fontSize: 11, fontWeight: 700, color: 'var(--color-text-muted)', letterSpacing: 1.2, textTransform: 'uppercase', display: 'block', marginBottom: 8 }}>
      {children}
    </span>
  );
}

export default function DetailScreen({ zombie, onBack, onDismissed }) {
  const { isDark } = useTheme();
  const { toast }         = useToast();
  const cfg               = serviceConfig(zombie.service);

  const [modalVisible, setModalVisible] = useState(false);
  const [modalAction, setModalAction]   = useState('dismiss');
  const [selectedReason, setSelectedReason] = useState('intentional');
  const [note, setNote]       = useState('');
  const [snoozeDays, setSnoozeDays] = useState(7);
  const [submitting, setSubmitting] = useState(false);
  const [restoreConfirmVisible, setRestoreConfirmVisible] = useState(false);
  const [restoring, setRestoring] = useState(false);

  const isDismissed = !!zombie.dismissal_id && zombie.dismiss_action === 'dismiss';
  const isSnoozed   = !!zombie.dismissal_id && zombie.dismiss_action === 'snooze';

  function openModal(action) {
    setModalAction(action);
    setSelectedReason('intentional');
    setNote('');
    setSnoozeDays(7);
    setModalVisible(true);
  }

  async function handleSubmit() {
    if (selectedReason === 'other' && !note.trim()) {
      toast('Please add a note when selecting "Other".', 'error');
      return;
    }
    setSubmitting(true);
    try {
      const snoozeUntil = modalAction === 'snooze'
        ? new Date(Date.now() + snoozeDays * 24 * 60 * 60 * 1000).toISOString()
        : undefined;
      await dismissZombie({
        accountId: zombie.internal_account_id, provider: zombie.provider, service: zombie.service,
        region: zombie.region, resourceId: zombie.resource_id, action: modalAction,
        reason: selectedReason, note: note.trim(), snoozeUntil,
      });
      setModalVisible(false);
      toast(
        modalAction === 'snooze' ? `Resource snoozed for ${snoozeDays} day${snoozeDays !== 1 ? 's' : ''}` : 'Resource dismissed',
        modalAction === 'snooze' ? 'info' : 'success',
      );
      if (onDismissed) onDismissed();
      onBack();
    } catch (err) {
      if (err.message === 'already_dismissed') {
        // Server says a dismissal already exists for this fingerprint but the
        // local resource list doesn't carry its id (stale or race). Open the
        // restore confirm modal which fetches the dismissal id and revokes.
        // Close the dismiss modal first — two overlapping Overlays would both
        // react to a single Escape and fight over focus restore.
        setModalVisible(false);
        setRestoreConfirmVisible(true);
      } else {
        toast('Something went wrong. Please try again.', 'error');
      }
    } finally {
      setSubmitting(false);
    }
  }

  async function handleRestore() {
    if (!zombie.dismissal_id) return;
    try {
      await revokeDismissal(zombie.dismissal_id);
      toast('Resource restored to zombie list', 'success');
      if (onDismissed) onDismissed();
      onBack();
    } catch {
      toast('Could not restore this resource. Please try again.', 'error');
    }
  }

  // Resolves the dismissal id for the current resource by fingerprint, then
  // revokes it. Used by the restore-confirm modal when the local zombie row
  // didn't carry dismissal_id (the "already_dismissed" 409 path on dismiss).
  async function handleRestoreFromConflict() {
    setRestoring(true);
    try {
      const dismissals = await fetchDismissals(zombie.internal_account_id);
      const match = dismissals.find(d =>
        d.provider === zombie.provider &&
        d.service === zombie.service &&
        d.region === zombie.region &&
        d.resource_id === zombie.resource_id,
      );
      if (!match) {
        // Dismissal already revoked elsewhere (concurrent operator action).
        // Refresh parent state and close the modal but DO NOT navigate back —
        // the user's intent was "restore"; we want them to see the page
        // re-render with Restore replaced by Dismiss/Snooze so they understand
        // the dismissal is already gone.
        toast('Dismissal already restored elsewhere — refreshed.', 'info');
        setRestoreConfirmVisible(false);
        setRestoring(false);
        if (onDismissed) onDismissed();
        return;
      }
      await revokeDismissal(match.id);
      toast('Resource restored to zombie list', 'success');
      // Clear local state before triggering navigation — once onBack() runs the
      // component unmounts and any subsequent setState would be a no-op (or a
      // React 18 unmount warning if logic changes).
      setRestoreConfirmVisible(false);
      setRestoring(false);
      if (onDismissed) onDismissed();
      onBack();
      return;
    } catch {
      toast('Could not restore this resource. Please try again.', 'error');
      setRestoring(false);
    }
  }

  const metricLabel = METRIC_LABELS[zombie.usage_metric] ?? zombie.usage_metric;

  const details = [
    { label: 'Provider',   value: zombie.provider },
    { label: 'Account ID', value: zombie.account_id },
    { label: 'Owner',      value: zombie.owner || '—' },
    { label: 'Period',     value: `${fmtDate(zombie.period_start)} → ${fmtDate(zombie.period_end)}` },
    { label: 'Resource ID', value: zombie.resource_id, mono: true },
    ...(zombie.arn ? [{ label: 'ARN', value: zombie.arn, mono: true }] : []),
  ];


  return (
    <>
      <div style={{ backgroundColor: 'var(--color-bg)', minHeight: '100%', paddingBottom: 48 }}>

        {/* Back button */}
        <div style={{ padding: '14px 20px 0' }}>
          <button onClick={onBack} style={{ padding: '4px 0', background: 'none', border: 'none', cursor: 'pointer' }}>
            <span style={{ color: 'var(--color-text-muted)', fontWeight: 600, fontSize: 14 }}>← Back to list</span>
          </button>
        </div>

        {/* Hero header */}
        <div style={{ backgroundColor: 'var(--color-surface-alt)', borderBottom: `1px solid var(--color-border)`, padding: '16px 20px 20px', marginTop: 8 }}>
          {/* Status badges */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 12, flexWrap: 'wrap' }}>
            <div style={{
              padding: '3px 8px',
              borderRadius: 6,
              backgroundColor: isDark ? cfg.darkBg : cfg.bg,
              border: `1px solid ${cfg.color}40`,
            }}>
              <span style={{ fontSize: 12, fontWeight: 800, color: cfg.color }}>{cfg.label}</span>
            </div>

            {zombie.is_zombie && !isDismissed && !isSnoozed && (
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}>
                <span style={{ width: 5, height: 5, borderRadius: '50%', backgroundColor: 'var(--color-zombie-badge-text)' }} />
                <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-zombie-badge-text)' }}>zombie</span>
              </span>
            )}
            {isDismissed && (
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}>
                <span style={{ width: 5, height: 5, borderRadius: '50%', backgroundColor: '#9CA3AF' }} />
                <span style={{ fontSize: 12, fontWeight: 600, color: '#9CA3AF' }}>dismissed</span>
              </span>
            )}
            {isSnoozed && (
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}>
                <span style={{ width: 5, height: 5, borderRadius: '50%', backgroundColor: '#2563EB' }} />
                <span style={{ fontSize: 12, fontWeight: 600, color: '#2563EB' }}>snoozed</span>
              </span>
            )}
          </div>

          {/* Cost */}
          <div style={{ marginBottom: 12 }}>
            <span style={{ fontSize: 32, fontWeight: 800, color: 'var(--color-accent)', letterSpacing: -0.5, display: 'block' }}>
              {zombie.currency} {zombie.monthly_cost.toFixed(2)}
              <span style={{ fontSize: 14, fontWeight: 500, color: 'var(--color-text-muted)', letterSpacing: 0, marginLeft: 4 }}>/mo</span>
            </span>
            {zombie.is_zombie && (
              <span style={{ fontSize: 13, color: 'var(--color-text-mid)', marginTop: 2, display: 'block' }}>
                Wasted budget — this resource is not being used
              </span>
            )}
          </div>

          {/* Key metrics row */}
          <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', marginBottom: 14 }}>
            {zombie.usage_metric && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: 0.8 }}>{metricLabel}</span>
                <span style={{ fontSize: 15, fontWeight: 700, color: 'var(--color-text)' }}>{zombie.usage_avg} {zombie.usage_unit}</span>
              </div>
            )}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: 0.8 }}>Region</span>
              <span style={{ fontSize: 15, fontWeight: 700, color: 'var(--color-text)' }}>{zombie.region}</span>
            </div>
            {(zombie.tags?.env) && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: 0.8 }}>Environment</span>
                <span style={{ fontSize: 15, fontWeight: 700, color: 'var(--color-text)' }}>{zombie.tags.env}</span>
              </div>
            )}
          </div>

          {/* Action buttons — show whenever the resource is actionable: an
              active zombie (offer Dismiss/Snooze) or a dismissed/snoozed
              resource (offer Restore). A dismissed resource may have dropped
              off the latest scan and not carry is_zombie=true; the dismissal
              state alone still warrants the Restore action. */}
          {(zombie.is_zombie || zombie.dismissal_id) && (
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
              {zombie.dismissal_id ? (
                <button
                  onClick={handleRestore}
                  style={{ padding: '9px 16px', borderRadius: 8, backgroundColor: `var(--color-success)20`, border: `1px solid var(--color-success)40`, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6 }}
                >
                  <span style={{ color: 'var(--color-success)', fontWeight: 700, fontSize: 13 }}>↩ Restore</span>
                </button>
              ) : (
                <>
                  <button
                    onClick={() => openModal('dismiss')}
                    style={{ padding: '9px 16px', borderRadius: 8, backgroundColor: 'var(--color-surface-raised)', border: `1px solid var(--color-border)`, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6 }}
                  >
                    <span style={{ color: 'var(--color-text-mid)', fontWeight: 700, fontSize: 13 }}>✓ Dismiss</span>
                  </button>
                  <button
                    onClick={() => openModal('snooze')}
                    style={{ padding: '9px 16px', borderRadius: 8, backgroundColor: isDark ? '#1e3a5f40' : '#DBEAFE', border: '1px solid #3b82f633', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6 }}
                  >
                    <span style={{ color: '#60a5fa', fontWeight: 700, fontSize: 13 }}>⏰ Snooze</span>
                  </button>
                </>
              )}
            </div>
          )}
        </div>

        <div style={{ padding: '20px 16px', display: 'flex', flexDirection: 'column', gap: 20 }}>
          {/* Detection reason */}
          {zombie.is_zombie && (
            <section aria-label="Detection reason">
              <SectionLabel>Why it was flagged</SectionLabel>
              <div style={{ backgroundColor: 'var(--color-surface)', border: `1px solid var(--color-border)`, borderLeft: `3px solid ${cfg.color}`, borderRadius: 10, padding: '14px 16px' }}>
                <span style={{ fontSize: 14, color: 'var(--color-text)', lineHeight: '22px', display: 'block' }}>{zombie.reason}</span>
              </div>
            </section>
          )}

          {/* Resource details table */}
          <section aria-label="Resource details">
            <SectionLabel>Resource Details</SectionLabel>
            <div style={{ backgroundColor: 'var(--color-surface)', border: `1px solid var(--color-border)`, borderRadius: 10, overflow: 'hidden' }}>
              {details.map(({ label, value, mono }, i) => (
                <div
                  key={label}
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'flex-start',
                    padding: '11px 16px',
                    borderBottom: i < details.length - 1 ? `1px solid var(--color-border)` : 'none',
                    gap: 16,
                  }}
                >
                  <span style={{ fontSize: 12, color: 'var(--color-text-muted)', fontWeight: 500, flexShrink: 0, paddingTop: 2 }}>{label}</span>
                  <span style={{
                    fontSize: mono ? 11 : 13,
                    color: 'var(--color-text)',
                    fontWeight: 600,
                    textAlign: 'right',
                    flex: 1,
                    fontFamily: mono ? '"Geist Mono Variable", monospace' : undefined,
                    // Mono values (resource ID, ARNs) are arbitrarily long;
                    // truncating them with ellipsis hides the most useful
                    // info. Allow break-all so they wrap inside the row.
                    // Non-mono values stay on a single line with ellipsis.
                    wordBreak: mono ? 'break-all' : undefined,
                    overflow: mono ? undefined : 'hidden',
                    textOverflow: mono ? undefined : 'ellipsis',
                  }}>
                    {value}
                  </span>
                </div>
              ))}
            </div>
          </section>

          {/* Remediation */}
          {zombie.is_zombie && (
            <section aria-label="Remediation steps">
              <SectionLabel>How to fix</SectionLabel>
              <div style={{ backgroundColor: 'var(--color-surface)', border: `1px solid var(--color-border)`, borderLeft: `3px solid ${cfg.color}`, borderRadius: 10, padding: '14px 16px' }}>
                <span style={{ fontSize: 14, color: 'var(--color-text)', lineHeight: '22px', display: 'block' }}>
                  {remediationHint(zombie.service, zombie.resource_id)}
                </span>
              </div>
              <CLICommand cmd={remediationCommand(zombie.service, zombie.resource_id, zombie.region)} />
            </section>
          )}
        </div>
      </div>

      {/* Dismiss / Snooze Modal */}
      <Overlay visible={modalVisible} onClose={() => setModalVisible(false)}>
        <div
          role="dialog"
          aria-modal="true"
          aria-label={modalAction === 'dismiss' ? 'Dismiss resource' : 'Snooze resource'}
          onClick={e => e.stopPropagation()}
          style={{
            backgroundColor: 'var(--color-surface)',
            borderRadius: 20,
            padding: 24,
            paddingBottom: 32,
            maxWidth: 480,
            width: '90vw',
            maxHeight: '85vh',
            overflowY: 'auto',
            boxShadow: '0 16px 40px rgba(0,0,0,0.3)',
          }}
        >
          <span style={{ fontSize: 18, fontWeight: 800, color: modalAction === 'dismiss' ? 'var(--color-error)' : 'var(--color-text)', display: 'block', marginBottom: 4 }}>
            {modalAction === 'dismiss' ? 'Dismiss Resource' : 'Snooze Resource'}
          </span>
          <span style={{ fontSize: 13, color: 'var(--color-text-muted)', display: 'block', marginBottom: 18 }}>
            {modalAction === 'dismiss'
              ? 'Resource will be hidden from the zombie list permanently.'
              : 'Resource will be hidden temporarily and resurface when the snooze expires.'}
          </span>

          <SectionLabel>Reason</SectionLabel>
          {DISMISS_REASONS.map(r => (
            <button
              key={r.value}
              onClick={() => setSelectedReason(r.value)}
              style={{
                display: 'flex', alignItems: 'center', gap: 12,
                padding: '10px 12px', borderRadius: 8, marginBottom: 4,
                border: `1px solid ${selectedReason === r.value ? 'var(--color-accent)' : 'var(--color-border)'}`,
                backgroundColor: selectedReason === r.value ? 'var(--color-accent-light)' : 'transparent',
                cursor: 'pointer', width: '100%', textAlign: 'left',
              }}
            >
              <div style={{
                width: 16, height: 16, borderRadius: '50%',
                border: `2px solid ${selectedReason === r.value ? 'var(--color-accent)' : 'var(--color-text-muted)'}`,
                backgroundColor: selectedReason === r.value ? 'var(--color-accent)' : 'transparent',
                flexShrink: 0,
              }} />
              <span style={{ fontSize: 14, color: selectedReason === r.value ? 'var(--color-accent)' : 'var(--color-text-mid)', fontWeight: selectedReason === r.value ? 600 : 400 }}>
                {r.label}
              </span>
            </button>
          ))}

          {(selectedReason === 'other' || note.length > 0) && (
            <textarea
              value={note}
              onChange={e => setNote(e.target.value)}
              placeholder={selectedReason === 'other' ? 'Note (required)…' : 'Add a note (optional)…'}
              aria-label="Additional note"
              style={{
                marginTop: 10,
                backgroundColor: 'var(--color-surface-raised)',
                border: `1px solid var(--color-border)`,
                borderRadius: 8,
                padding: 12,
                color: 'var(--color-text)',
                fontSize: 14,
                minHeight: 64,
                width: '100%',
                boxSizing: 'border-box',
                resize: 'vertical',
              }}
            />
          )}

          {modalAction === 'snooze' && (
            <div style={{ marginTop: 16 }}>
              <SectionLabel>Snooze for</SectionLabel>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                {SNOOZE_OPTIONS.map(o => (
                  <button
                    key={o.days}
                    onClick={() => setSnoozeDays(o.days)}
                    style={{
                      padding: '7px 14px', borderRadius: 20,
                      border: `1px solid ${snoozeDays === o.days ? 'var(--color-accent)' : 'var(--color-border)'}`,
                      backgroundColor: snoozeDays === o.days ? 'var(--color-accent-light)' : 'var(--color-surface-raised)',
                      cursor: 'pointer',
                    }}
                  >
                    <span style={{ fontSize: 13, color: snoozeDays === o.days ? 'var(--color-accent)' : 'var(--color-text-mid)', fontWeight: 600 }}>
                      {o.label}
                    </span>
                  </button>
                ))}
              </div>
            </div>
          )}

          <div style={{ display: 'flex', gap: 10, marginTop: 22 }}>
            <button
              onClick={() => setModalVisible(false)}
              style={{ flex: 1, padding: '13px', borderRadius: 10, border: `1px solid var(--color-border)`, backgroundColor: 'transparent', cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center' }}
            >
              <span style={{ color: 'var(--color-text-mid)', fontWeight: 700, fontSize: 14 }}>Cancel</span>
            </button>
            <button
              onClick={handleSubmit}
              disabled={submitting}
              style={{ flex: 1, padding: '13px', borderRadius: 10, backgroundColor: 'var(--color-accent)', border: 'none', cursor: 'pointer', opacity: submitting ? 0.6 : 1, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
            >
              {submitting
                ? <Spinner size={20} color={'var(--color-text-on-dark)'} />
                : <span style={{ color: 'var(--color-text-on-dark)', fontWeight: 800, fontSize: 14 }}>{modalAction === 'dismiss' ? 'Dismiss' : 'Snooze'}</span>
              }
            </button>
          </div>
        </div>
      </Overlay>

      {/* Restore-from-conflict Modal — opens when a dismiss attempt 409s because
          a dismissal already exists server-side. Offers a one-click restore
          (resolves the dismissal id by fingerprint, then revokes). */}
      <Overlay visible={restoreConfirmVisible} onClose={() => !restoring && setRestoreConfirmVisible(false)}>
        <div
          role="dialog"
          aria-modal="true"
          aria-label="Already dismissed"
          onClick={e => e.stopPropagation()}
          style={{
            backgroundColor: 'var(--color-surface)',
            borderRadius: 20,
            padding: 24,
            paddingBottom: 32,
            maxWidth: 420,
            width: '90vw',
            boxShadow: '0 16px 40px rgba(0,0,0,0.3)',
          }}
        >
          <span style={{ fontSize: 18, fontWeight: 800, color: 'var(--color-text)', display: 'block', marginBottom: 8 }}>
            Already dismissed
          </span>
          <span style={{ fontSize: 13, color: 'var(--color-text-mid)', display: 'block', marginBottom: 22, lineHeight: 1.5 }}>
            This resource already has an active dismissal. Restore it to make changes — it will return to the zombie list.
          </span>
          <div style={{ display: 'flex', gap: 10 }}>
            <button
              onClick={() => setRestoreConfirmVisible(false)}
              disabled={restoring}
              style={{ flex: 1, padding: '13px', borderRadius: 10, border: `1px solid var(--color-border)`, backgroundColor: 'transparent', cursor: restoring ? 'not-allowed' : 'pointer', opacity: restoring ? 0.6 : 1, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
            >
              <span style={{ color: 'var(--color-text-mid)', fontWeight: 700, fontSize: 14 }}>Cancel</span>
            </button>
            <button
              onClick={handleRestoreFromConflict}
              disabled={restoring}
              style={{ flex: 1, padding: '13px', borderRadius: 10, backgroundColor: 'var(--color-success)', border: 'none', cursor: restoring ? 'not-allowed' : 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center' }}
            >
              {restoring
                ? <Spinner size={20} color={'var(--color-text-on-dark)'} />
                : <span style={{ color: 'var(--color-text-on-dark)', fontWeight: 800, fontSize: 14 }}>↩ Restore</span>
              }
            </button>
          </div>
        </div>
      </Overlay>
    </>
  );
}
