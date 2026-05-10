import { useState } from 'react';
import { useQueryClient, useMutation } from '@tanstack/react-query';
import { updateAccount, deleteAccount, scanAccount } from '../api/client';
import { useTheme } from '../theme/ThemeContext';
import { useToast } from '../context/ToastContext';
import { useScanStatus } from '../hooks/useScanStatus';
import { Spinner } from '../components/primitives';
import { useDestructiveConfirm, DestructiveConfirmModal } from '../components/DestructiveConfirm';

function Field({ label, value, onChange, placeholder, mono, type = 'text', hint, theme }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <label style={{ fontSize: 13, fontWeight: 600, color: theme.textMid }}>{label}</label>
      <input
        style={{
          backgroundColor: theme.surfaceAlt,
          border: `1px solid ${theme.border}`,
          borderRadius: 8,
          padding: '10px 12px',
          fontSize: 14,
          color: theme.text,
          outline: 'none',
          fontFamily: mono ? '"Geist Mono Variable", monospace' : undefined,
        }}
        value={value}
        onChange={e => onChange(e.target.value)}
        placeholder={placeholder}
        autoCapitalize="none"
        autoCorrect="off"
        type={type}
      />
      {hint && <span style={{ fontSize: 12, color: theme.textMuted, fontStyle: 'italic' }}>{hint}</span>}
    </div>
  );
}

function StatusBadge({ status, theme }) {
  // Inline dot + label — no pill chrome. The colored dot is the at-a-glance
  // indicator; the headline beside it carries the operational message.
  const config = {
    connected:            { color: theme.success, label: 'Connected' },
    error:                { color: theme.error,   label: 'Disconnected' },
    scan_timeout:         { color: theme.warning, label: 'Timed Out' },
    circuit_breaker_open: { color: theme.warning, label: 'Paused' },
    scanning:             { color: theme.accent,  label: 'Scanning…' },
  };
  const c = config[status] ?? { color: theme.textMuted, label: 'Unknown' };

  return (
    <div style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
      <div style={{ width: 8, height: 8, borderRadius: '50%', backgroundColor: c.color, flexShrink: 0 }} />
      <span style={{ fontSize: 13, fontWeight: 700, color: c.color }}>{c.label}</span>
    </div>
  );
}

// One-line affirmation/diagnosis paired with the chip. Mirrors the License
// page pattern (chip = state, sentence = consequence) so screen-reader users
// hear the meaning, not just the state name.
function statusHeadline(status) {
  switch (status) {
    case 'connected':            return 'Connection healthy. Scans run on schedule.';
    case 'error':                return 'Last scan failed — check credentials and region.';
    case 'scan_timeout':         return 'Last scan timed out at 15 minutes.';
    case 'circuit_breaker_open': return 'Paused after repeated scan failures.';
    case 'scanning':             return 'Scan in progress.';
    default:                     return '';
  }
}

export default function AccountSettingsScreen({ account, onBack, onAccountUpdated, onAccountDeleted }) {
  const { theme }   = useTheme();
  const { toast }   = useToast();
  const { watch }   = useScanStatus();
  const queryClient = useQueryClient();

  const [label, setLabel]             = useState(account?.label ?? '');
  const [accessKeyId, setAccessKeyId] = useState(account?.access_key_id ?? '');
  const [secretKey, setSecretKey]     = useState('');
  const [region, setRegion]           = useState(account?.region ?? 'eu-central-1');
  const [scanIntervalHours, setScanIntervalHours] = useState(account?.scan_interval_hours?.toString() ?? '24');
  const [loading, setLoading]         = useState(false);
  const [error, setError]             = useState('');
  const scanning = account?.status === 'scanning';
  const accountName = account ? (account.label || account.access_key_id.slice(0, 8) + '…') : '';

  // Type-to-confirm delete flow — same UX as Profile / Organization
  // destructive flows. The user must type the account name before the
  // Delete button enables.
  const deleteCtrl = useDestructiveConfirm({
    target: accountName,
    mutationFn: () => deleteAccount(account.id),
    successMessage: 'Account deleted.',
    onSuccess: () => onAccountDeleted(account.id),
    toast,
  });

  async function handleSave() {
    if (!accessKeyId.trim()) { setError('Access Key ID is required.'); return; }
    const scanInterval = parseInt(scanIntervalHours, 10);
    if (isNaN(scanInterval) || scanInterval < 0) { setError('Scan interval must be a number ≥ 0.'); return; }
    setError('');
    setLoading(true);
    try {
      const result = await updateAccount(account.id, {
        label: label.trim() || 'My AWS Account',
        accessKeyId: accessKeyId.trim(),
        secretKey: secretKey.trim() || undefined,
        region: region.trim() || 'eu-central-1',
        scan_interval_hours: scanInterval,
      });
      toast('Account settings saved', 'success');
      onAccountUpdated(result);
    } catch {
      setError('Failed to update. Check your credentials and try again.');
    } finally {
      setLoading(false);
    }
  }

  const scanMutation = useMutation({
    mutationFn: scanAccount,
    onMutate: async (accountId) => {
      await queryClient.cancelQueries({ queryKey: ['accounts'] });
      const previous = queryClient.getQueryData(['accounts']);
      const displayLabel = account.label ?? accountId.slice(0, 8);

      queryClient.setQueryData(['accounts'], (accs = []) =>
        accs.map(a => a.id === accountId ? { ...a, status: 'scanning' } : a),
      );
      toast(`Starting scan for ${displayLabel}…`, 'info');
      return { previous, displayLabel };
    },
    onError: (err, accountId, ctx) => {
      if (err?.code === 'already_scanning') {
        toast(`Scan already running for ${ctx?.displayLabel ?? 'account'}`, 'info');
        watch(accountId, { label: account.label, onEnd: () => onAccountUpdated(account) });
        return;
      }
      // B1.6 slice 8 — license scan-gate. Roll back the optimistic 'scanning'
      // status (the scan never actually started) and surface a toast that
      // names the renewal contact. Per plan §4.9.2b the button stays
      // clickable so the user gets a clear explanation rather than a
      // mystery-disabled control.
      if (err?.code === 'license_expired') {
        if (ctx?.previous) queryClient.setQueryData(['accounts'], ctx.previous);
        toast('License expired — scans paused. Contact sales@axiaops.io to renew.', 'error');
        return;
      }
      if (ctx?.previous) queryClient.setQueryData(['accounts'], ctx.previous);
      toast(`Couldn't start scan for ${ctx?.displayLabel ?? 'account'}`, 'error');
    },
    onSuccess: (_data, accountId) => {
      watch(accountId, { label: account.label, onEnd: () => onAccountUpdated(account) });
    },
  });

  const handleScan = () => scanMutation.mutate(account.id);

  const t = theme;

  return (
    <div style={{ minHeight: '100%', backgroundColor: t.bg }}>
      <div style={{ maxWidth: 560, margin: '0 auto', padding: '0 0 64px' }}>

        {/* Header */}
        <div style={{ padding: '20px 20px 16px', borderBottom: `1px solid ${t.border}` }}>
          <button onClick={onBack} style={{ padding: '4px 0', background: 'none', border: 'none', cursor: 'pointer', marginBottom: 12 }}>
            <span style={{ color: t.accent, fontSize: 14, fontWeight: 600 }}>← Back</span>
          </button>
          <h1 style={{ fontSize: 20, fontWeight: 800, color: t.text, margin: '0 0 4px' }}>{accountName}</h1>
          <p style={{ fontSize: 13, color: t.textMuted, margin: 0 }}>{account.region}</p>
        </div>

        <div style={{ padding: '20px' }}>
          {/* Status + quick actions */}
          <div style={{
            backgroundColor: t.surface,
            border: `1px solid ${t.border}`,
            borderRadius: 12,
            padding: 20,
            marginBottom: 16,
            display: 'flex',
            flexDirection: 'column',
            gap: 14,
          }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 10 }}>
                <StatusBadge status={account.status} theme={t} />
                <span style={{ fontSize: 13, color: t.text, fontWeight: 600 }}>
                  {statusHeadline(account.status)}
                </span>
              </div>
              {account.last_scanned_at && (
                <span style={{ fontSize: 12, color: t.textMuted }}>
                  Last scan: {new Date(account.last_scanned_at).toLocaleString()}
                </span>
              )}
            </div>

            {account.status === 'circuit_breaker_open' && (
              <div style={{ backgroundColor: `${t.warning}18`, border: `1px solid ${t.warning}40`, borderRadius: 8, padding: '10px 12px' }}>
                <span style={{ fontSize: 13, color: t.warning, lineHeight: '20px', display: 'block' }}>
                  Too many consecutive scan failures. Wait a few minutes before retrying, or check your IAM credentials.
                </span>
              </div>
            )}

            {/* Actions row */}
            <div style={{ display: 'flex', gap: 10 }}>
              <button
                onClick={handleScan}
                disabled={scanning || account.status === 'circuit_breaker_open'}
                aria-label="Run scan now"
                style={{
                  flex: 1,
                  padding: '11px',
                  borderRadius: 8,
                  backgroundColor: t.accent,
                  border: 'none',
                  cursor: scanning || account.status === 'circuit_breaker_open' ? 'not-allowed' : 'pointer',
                  opacity: scanning || account.status === 'circuit_breaker_open' ? 0.6 : 1,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  gap: 6,
                }}
              >
                {scanning ? <Spinner size={16} color="#fff" /> : (
                  <>
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#fff" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                      <polyline points="23 4 23 10 17 10" /><polyline points="1 20 1 14 7 14" />
                      <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
                    </svg>
                    <span style={{ color: '#fff', fontSize: 14, fontWeight: 700 }}>Scan Now</span>
                  </>
                )}
              </button>
              {/* Destructive primary — filled red, white label. Mirrors the
                  Delete Organization button (DangerSection in Profile.jsx /
                  Organization.jsx) so destructive primaries look the same
                  across the app. Type-to-confirm modal still gates the action. */}
              <button
                onClick={deleteCtrl.openModal}
                disabled={deleteCtrl.isPending}
                aria-label="Delete account"
                style={{
                  padding: '11px 16px',
                  borderRadius: 8,
                  backgroundColor: t.error,
                  border: 'none',
                  cursor: deleteCtrl.isPending ? 'not-allowed' : 'pointer',
                  opacity: deleteCtrl.isPending ? 0.6 : 1,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  gap: 6,
                }}
              >
                {deleteCtrl.isPending ? <Spinner size={16} color="#fff" /> : (
                  <span style={{ color: '#fff', fontSize: 14, fontWeight: 700 }}>Delete</span>
                )}
              </button>
            </div>
          </div>

          {/* Edit form */}
          <div style={{
            backgroundColor: t.surface,
            border: `1px solid ${t.border}`,
            borderRadius: 12,
            padding: 20,
            display: 'flex',
            flexDirection: 'column',
            gap: 18,
          }}>
            <span style={{ fontSize: 15, fontWeight: 700, color: t.text }}>Account Details</span>

            <Field label="Label" value={label} onChange={setLabel} placeholder="e.g. Production AWS" theme={t} />
            <Field label="AWS Access Key ID" value={accessKeyId} onChange={setAccessKeyId} placeholder="AKIAIOSFODNN7EXAMPLE" mono theme={t} />
            <Field label="AWS Secret Access Key" value={secretKey} onChange={setSecretKey} placeholder="Leave blank to keep existing" mono type="password" theme={t} />
            <Field label="Region" value={region} onChange={setRegion} placeholder="eu-central-1" mono theme={t} />
            <Field
              label="Auto-scan interval (hours)"
              value={scanIntervalHours}
              onChange={setScanIntervalHours}
              placeholder="24"
              type="number"
              hint="0 = on-demand only, or enter hours between automatic scans"
              theme={t}
            />

            {error && (
              <div style={{ backgroundColor: `${t.error}18`, border: `1px solid ${t.error}40`, borderRadius: 8, padding: '10px 12px' }}>
                <span style={{ fontSize: 13, color: t.error, fontWeight: 500 }}>{error}</span>
              </div>
            )}

            <button
              onClick={handleSave}
              disabled={loading}
              style={{
                backgroundColor: t.accent,
                borderRadius: 10,
                padding: '14px',
                border: 'none',
                cursor: loading ? 'not-allowed' : 'pointer',
                opacity: loading ? 0.65 : 1,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              {loading ? <Spinner size={20} color="#fff" /> : <span style={{ color: '#fff', fontSize: 15, fontWeight: 700 }}>Save Changes</span>}
            </button>
          </div>
        </div>
      </div>

      <DestructiveConfirmModal
        ctrl={deleteCtrl}
        title="Delete Account?"
        warning={`This permanently deletes the cloud account "${accountName}", all its scan history, and every resource record tied to it. Cannot be undone.`}
        targetLabel="account name"
        confirmLabel="Delete Account"
      />
    </div>
  );
}
