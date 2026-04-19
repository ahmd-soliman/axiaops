import { useState } from 'react';
import { serviceConfig } from '../components/serviceConfig';
import { useTheme } from '../theme/ThemeContext';
import { dismissGhost, revokeDismissal } from '../api/client';
import { Overlay, Spinner } from '../components/primitives';

const DISMISS_REASONS = [
  { value: 'intentional', label: 'Intentionally idle' },
  { value: 'scheduled_deletion', label: 'Scheduled for deletion' },
  { value: 'false_positive', label: 'False positive' },
  { value: 'cost_accepted', label: 'Cost accepted' },
  { value: 'other', label: 'Other (add note)' },
];

const SNOOZE_OPTIONS = [
  { label: '1 day',   days: 1 },
  { label: '7 days',  days: 7 },
  { label: '30 days', days: 30 },
  { label: '90 days', days: 90 },
];

function fmtDate(iso) {
  return new Date(iso).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
}

function friendlyMetricName(metric) {
  const map = {
    'CPUUtilization': 'CPU Usage',
    'DatabaseConnections': 'DB Connections',
    'Invocations': 'Invocations',
    'RequestCount': 'Requests',
    'BytesOutToDestination': 'Data Transfer',
    'NetworkInterfaceAttachment': 'Attachment',
  };
  return map[metric] || metric;
}

function remediationCommand(service, resourceId = '', region = 'eu-central-1') {
  const r = `--region ${region}`;
  if (service === 'AmazonVPC') {
    if (resourceId.startsWith('eipalloc-')) return `aws ec2 release-address --allocation-id ${resourceId} ${r}`;
    return `aws ec2 delete-nat-gateway --nat-gateway-id ${resourceId} ${r}`;
  }
  const cmds = {
    AmazonEC2: `aws ec2 stop-instances --instance-ids ${resourceId} ${r}`,
    AmazonRDS: `aws rds delete-db-instance --db-instance-identifier ${resourceId} --skip-final-snapshot ${r}`,
    AWSLambda: `aws lambda delete-function --function-name ${resourceId} ${r}`,
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
    AmazonEC2: 'Stop or terminate the instance. If it is part of an Auto Scaling group, remove it from the group first.',
    AmazonRDS: 'Create a final snapshot, then delete the DB instance. Confirm with the owner before deleting.',
    AWSLambda: 'Delete the function. Check for any EventBridge rules or triggers pointing to it first.',
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
    <div style={{ marginTop: 10, backgroundColor: '#0d1117', borderRadius: 10, border: '1px solid #30363d' }}>
      <div style={{ position: 'relative', backgroundColor: '#0f172a', borderRadius: 6, padding: 12, border: '1px solid #334155' }}>
        <span style={{ fontFamily: 'monospace', fontSize: 13, color: '#e2e8f0', lineHeight: '20px', display: 'block', paddingRight: 32 }}>{cmd}</span>
        <button
          onClick={handleCopy}
          style={{ position: 'absolute', top: 12, right: 12, padding: 4, background: 'none', border: 'none', cursor: 'pointer', fontSize: 16, color: '#ffffff' }}
        >
          {copied ? '✓' : '⧉'}
        </button>
      </div>
    </div>
  );
}

export default function DetailScreen({ ghost, onBack, onDismissed }) {
  const { theme } = useTheme();
  const cfg = serviceConfig(ghost.service);

  const [modalVisible, setModalVisible] = useState(false);
  const [modalAction, setModalAction]   = useState('dismiss');
  const [selectedReason, setSelectedReason] = useState('intentional');
  const [note, setNote]       = useState('');
  const [snoozeDays, setSnoozeDays] = useState(7);
  const [submitting, setSubmitting] = useState(false);

  const isDismissed = !!ghost.dismissal_id && ghost.dismiss_action === 'dismiss';
  const isSnoozed   = !!ghost.dismissal_id && ghost.dismiss_action === 'snooze';

  function openModal(action) {
    setModalAction(action);
    setSelectedReason('intentional');
    setNote('');
    setSnoozeDays(7);
    setModalVisible(true);
  }

  async function handleSubmit() {
    if (selectedReason === 'other' && !note.trim()) {
      window.alert('Please add a note when selecting "Other".');
      return;
    }
    setSubmitting(true);
    try {
      const snoozeUntil = modalAction === 'snooze'
        ? new Date(Date.now() + snoozeDays * 24 * 60 * 60 * 1000).toISOString()
        : undefined;
      await dismissGhost({
        accountId: ghost.internal_account_id, provider: ghost.provider, service: ghost.service,
        region: ghost.region, resourceId: ghost.resource_id, action: modalAction,
        reason: selectedReason, note: note.trim(), snoozeUntil,
      });
      setModalVisible(false);
      if (onDismissed) onDismissed();
      onBack();
    } catch (err) {
      const msg = err.message === 'already_dismissed'
        ? 'This resource is already dismissed. Restore it first.'
        : 'Something went wrong. Please try again.';
      window.alert(msg);
    } finally {
      setSubmitting(false);
    }
  }

  async function handleRestore() {
    if (!ghost.dismissal_id) return;
    try {
      await revokeDismissal(ghost.dismissal_id);
      if (onDismissed) onDismissed();
      onBack();
    } catch {
      window.alert('Could not restore this resource. Please try again.');
    }
  }

  const stats = [
    { label: 'Monthly Cost', value: `${ghost.currency} ${ghost.monthly_cost.toFixed(2)}`, accent: true },
    { label: friendlyMetricName(ghost.usage_metric), value: `${ghost.usage_avg} ${ghost.usage_unit}` },
    { label: 'Region', value: ghost.region },
    { label: 'Environment', value: ghost.tags?.env ?? 'Not tagged' },
  ];

  const details = [
    { label: 'Provider',    value: ghost.provider },
    { label: 'Account ID',  value: ghost.account_id },
    { label: 'Owner',       value: ghost.owner },
    { label: 'Period',      value: `${fmtDate(ghost.period_start)} → ${fmtDate(ghost.period_end)}` },
    { label: 'Resource ID', value: ghost.resource_id, mono: true },
    ...(ghost.arn ? [{ label: 'ARN', value: ghost.arn, mono: true }] : []),
  ];

  const t = theme;

  return (
    <>
      <div style={{ flex: 1, backgroundColor: t.bg, minHeight: '100vh', overflowY: 'auto' }}>
        <div style={{ paddingBottom: 48 }}>
          {/* Header */}
          <div style={{ backgroundColor: t.surfaceAlt, paddingBottom: 24, borderBottom: `1px solid ${t.border}` }}>
            <button onClick={onBack} style={{ paddingLeft: 20, paddingTop: 16, paddingBottom: 12, background: 'none', border: 'none', cursor: 'pointer' }}>
              <span style={{ color: t.textMuted, fontWeight: 600, fontSize: 14 }}>← Back to list</span>
            </button>
            <div style={{ paddingLeft: 20, paddingRight: 20 }}>
              <div style={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 8, marginBottom: 10 }}>
                <div style={{ paddingLeft: 8, paddingRight: 8, paddingTop: 3, paddingBottom: 3, borderRadius: 5, backgroundColor: cfg.color }}>
                  <span style={{ color: '#FFFFFF', fontSize: 11, fontWeight: 800 }}>{cfg.label}</span>
                </div>
                {ghost.is_ghost && (
                  <div style={{ backgroundColor: t.ghostBadgeBg, paddingLeft: 5, paddingRight: 5, paddingTop: 2, paddingBottom: 2, borderRadius: 4 }}>
                    <span style={{ fontSize: 9, fontWeight: 700, color: t.ghostBadgeText, textTransform: 'uppercase' }}>Zombie</span>
                  </div>
                )}
                {isDismissed && (
                  <div style={{ backgroundColor: '#374151', paddingLeft: 5, paddingRight: 5, paddingTop: 2, paddingBottom: 2, borderRadius: 4 }}>
                    <span style={{ fontSize: 9, fontWeight: 700, color: '#9CA3AF', textTransform: 'uppercase' }}>Dismissed</span>
                  </div>
                )}
                {isSnoozed && (
                  <div style={{ backgroundColor: '#1e3a5f', paddingLeft: 5, paddingRight: 5, paddingTop: 2, paddingBottom: 2, borderRadius: 4 }}>
                    <span style={{ fontSize: 9, fontWeight: 700, color: '#60a5fa', textTransform: 'uppercase' }}>Snoozed</span>
                  </div>
                )}
              </div>
              <span style={{ color: t.text, fontSize: 15, fontWeight: 500, display: 'block', marginBottom: 8, opacity: 0.7 }}>{ghost.service}</span>
              <span style={{ color: t.accent, fontSize: 38, fontWeight: 800, letterSpacing: -1, display: 'block' }}>{ghost.currency} {ghost.monthly_cost.toFixed(2)}</span>
              <span style={{ color: t.textMid, fontSize: 13, marginTop: 4, marginBottom: 12, display: 'block' }}>{ghost.is_ghost ? '💸 Wasted per month' : 'Monthly cost'}</span>

              {/* Stats chips in header */}
              <div style={{ display: 'flex', flexDirection: 'row', flexWrap: 'wrap', gap: 8, marginBottom: 12 }}>
                {stats.slice(1).map(({ label, value }) => (
                  <div key={label} style={{ backgroundColor: 'rgba(251, 146, 60, 0.15)', borderRadius: 8, paddingLeft: 12, paddingRight: 12, paddingTop: 6, paddingBottom: 6, border: '1px solid rgba(251, 146, 60, 0.3)', display: 'flex', flexDirection: 'column', alignItems: 'center', textAlign: 'center' }}>
                    <span style={{ fontSize: 14, fontWeight: 600, color: t.text }}>{value}</span>
                    <span style={{ fontSize: 11, color: t.textMuted }}>{label}</span>
                  </div>
                ))}
              </div>

              {ghost.is_ghost && (
                <div style={{ display: 'flex', flexDirection: 'row', gap: 8, marginTop: 4 }}>
                  {ghost.dismissal_id ? (
                    <button onClick={handleRestore} style={{ paddingLeft: 14, paddingRight: 14, paddingTop: 9, paddingBottom: 9, borderRadius: 8, backgroundColor: '#14532d', border: 'none', cursor: 'pointer' }}>
                      <span style={{ color: '#f3f4f6', fontWeight: 700, fontSize: 13 }}>↩ Restore</span>
                    </button>
                  ) : (
                    <>
                      <button onClick={() => openModal('dismiss')} style={{ paddingLeft: 14, paddingRight: 14, paddingTop: 9, paddingBottom: 9, borderRadius: 8, backgroundColor: '#374151', border: 'none', cursor: 'pointer' }}>
                        <span style={{ color: '#f3f4f6', fontWeight: 700, fontSize: 13 }}>✓ Dismiss</span>
                      </button>
                      <button onClick={() => openModal('snooze')} style={{ paddingLeft: 14, paddingRight: 14, paddingTop: 9, paddingBottom: 9, borderRadius: 8, backgroundColor: '#1e3a5f', border: 'none', cursor: 'pointer' }}>
                        <span style={{ color: '#f3f4f6', fontWeight: 700, fontSize: 13 }}>⏰ Snooze</span>
                      </button>
                    </>
                  )}
                </div>
              )}
            </div>
          </div>

          {/* Why flagged */}
          {ghost.is_ghost && (
            <div style={{ paddingLeft: 16, paddingRight: 16, marginBottom: 16 }}>
              <span style={{ fontSize: 11, fontWeight: 700, color: t.textMuted, letterSpacing: 1.2, textTransform: 'uppercase', marginBottom: 8, display: 'block' }}>Detection Reason</span>
              <div style={{ backgroundColor: t.card, borderLeft: `3px solid ${cfg.color}`, borderRadius: 8, padding: 14 }}>
                <span style={{ fontSize: 14, color: t.text, lineHeight: '21px', display: 'block' }}>{ghost.reason}</span>
              </div>
            </div>
          )}

          {/* Resource details */}
          <div style={{ paddingLeft: 16, paddingRight: 16, marginBottom: 16 }}>
            <span style={{ fontSize: 11, fontWeight: 700, color: t.textMuted, letterSpacing: 1.2, textTransform: 'uppercase', marginBottom: 8, display: 'block' }}>Resource Details</span>
            <div style={{ backgroundColor: t.card, borderRadius: 10, overflow: 'hidden' }}>
              {details.map(({ label, value, mono }, i) => (
                <div key={label} style={{ display: 'flex', flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center', paddingLeft: 14, paddingRight: 14, paddingTop: 11, paddingBottom: 11, borderBottom: i < details.length - 1 ? `1px solid ${t.border}` : 'none' }}>
                  <span style={{ fontSize: 12, color: t.textMuted, fontWeight: 500 }}>{label}</span>
                  <span style={{ fontSize: mono ? 11 : 13, color: t.text, fontWeight: 600, textAlign: 'right', flex: 1, marginLeft: 16, fontFamily: mono ? 'monospace' : undefined, overflow: 'hidden', textOverflow: 'ellipsis' }}>{value}</span>
                </div>
              ))}
            </div>
          </div>

          {/* Suggested action */}
          {ghost.is_ghost && (
            <div style={{ paddingLeft: 16, paddingRight: 16, marginBottom: 16 }}>
              <span style={{ fontSize: 11, fontWeight: 700, color: t.textMuted, letterSpacing: 1.2, textTransform: 'uppercase', marginBottom: 8, display: 'block' }}>How to Fix</span>
              <div style={{ backgroundColor: t.accentLight, borderRadius: 10, padding: 14, display: 'flex', flexDirection: 'row', alignItems: 'flex-start', gap: 10, border: `1px solid ${t.accentBorder}` }}>
                <span style={{ fontSize: 16 }}>⚡</span>
                <span style={{ fontSize: 13, color: t.accentText, lineHeight: '20px', flex: 1, display: 'block' }}>{remediationHint(ghost.service, ghost.resource_id)}</span>
              </div>
              <CLICommand cmd={remediationCommand(ghost.service, ghost.resource_id, ghost.region)} />
            </div>
          )}
        </div>
      </div>

      {/* Dismiss / Snooze Modal */}
      <Overlay visible={modalVisible} onClose={() => setModalVisible(false)}>
        <div style={{ backgroundColor: t.surface, borderRadius: 20, padding: 24, paddingBottom: 36, maxWidth: 480, width: '90vw', maxHeight: '80vh', overflowY: 'auto' }}>
          <span style={{ fontSize: 18, fontWeight: 800, color: t.text, marginBottom: 4, display: 'block' }}>
            {modalAction === 'dismiss' ? 'Dismiss Resource' : 'Snooze Resource'}
          </span>
          <span style={{ fontSize: 13, color: t.textMuted, marginBottom: 18, display: 'block' }}>
            {modalAction === 'dismiss' ? 'This resource will be hidden from the ghost list.' : 'This resource will be hidden temporarily.'}
          </span>

          <span style={{ fontSize: 11, fontWeight: 700, color: t.textMuted, letterSpacing: 1, textTransform: 'uppercase', marginBottom: 8, marginTop: 4, display: 'block' }}>Reason</span>
          {DISMISS_REASONS.map((r) => (
            <button
              key={r.value}
              onClick={() => setSelectedReason(r.value)}
              style={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 12, paddingTop: 10, paddingBottom: 10, paddingLeft: 12, paddingRight: 12, borderRadius: 8, marginBottom: 4, border: `1px solid ${selectedReason === r.value ? t.accent : t.border}`, backgroundColor: selectedReason === r.value ? t.accentLight : 'transparent', cursor: 'pointer', width: '100%', textAlign: 'left' }}
            >
              <div style={{ width: 16, height: 16, borderRadius: '50%', border: `2px solid ${selectedReason === r.value ? t.accent : t.textMuted}`, backgroundColor: selectedReason === r.value ? t.accent : 'transparent', flexShrink: 0 }} />
              <span style={{ fontSize: 14, color: selectedReason === r.value ? t.accent : t.textMid, fontWeight: selectedReason === r.value ? 600 : 400 }}>{r.label}</span>
            </button>
          ))}

          {(selectedReason === 'other' || note.length > 0) && (
            <textarea
              style={{ marginTop: 10, backgroundColor: t.card, borderRadius: 8, border: `1px solid ${t.border}`, padding: 12, color: t.text, fontSize: 14, minHeight: 60, width: '100%', boxSizing: 'border-box', resize: 'vertical', outline: 'none' }}
              placeholder={selectedReason === 'other' ? 'Note (required)…' : 'Add a note (optional)…'}
              value={note}
              onChange={(e) => setNote(e.target.value)}
            />
          )}

          {modalAction === 'snooze' && (
            <>
              <span style={{ fontSize: 11, fontWeight: 700, color: t.textMuted, letterSpacing: 1, textTransform: 'uppercase', marginBottom: 8, marginTop: 4, display: 'block' }}>Snooze for</span>
              <div style={{ display: 'flex', flexDirection: 'row', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
                {SNOOZE_OPTIONS.map((o) => (
                  <button
                    key={o.days}
                    onClick={() => setSnoozeDays(o.days)}
                    style={{ paddingLeft: 14, paddingRight: 14, paddingTop: 7, paddingBottom: 7, borderRadius: 20, border: `1px solid ${snoozeDays === o.days ? t.accent : t.border}`, backgroundColor: snoozeDays === o.days ? t.accentLight : t.surfaceRaised, cursor: 'pointer' }}
                  >
                    <span style={{ fontSize: 13, color: snoozeDays === o.days ? t.accent : t.textMid, fontWeight: 600 }}>{o.label}</span>
                  </button>
                ))}
              </div>
            </>
          )}

          <div style={{ display: 'flex', flexDirection: 'row', gap: 12, marginTop: 20 }}>
            <button onClick={() => setModalVisible(false)} style={{ flex: 1, paddingTop: 13, paddingBottom: 13, borderRadius: 10, display: 'flex', alignItems: 'center', justifyContent: 'center', border: `1px solid ${t.border}`, backgroundColor: 'transparent', cursor: 'pointer' }}>
              <span style={{ color: t.textMid, fontWeight: 700, fontSize: 15 }}>Cancel</span>
            </button>
            <button onClick={handleSubmit} disabled={submitting} style={{ flex: 1, paddingTop: 13, paddingBottom: 13, borderRadius: 10, display: 'flex', alignItems: 'center', justifyContent: 'center', backgroundColor: t.accent, border: 'none', cursor: 'pointer', opacity: submitting ? 0.6 : 1 }}>
              {submitting ? <Spinner size={20} color="#fff" /> : <span style={{ color: '#fff', fontWeight: 800, fontSize: 15 }}>{modalAction === 'dismiss' ? 'Dismiss' : 'Snooze'}</span>}
            </button>
          </div>
        </div>
      </Overlay>
    </>
  );
}
