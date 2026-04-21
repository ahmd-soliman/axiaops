import { useState } from 'react';
import { updateAccount, deleteAccount, scanAccount } from '../api/client';
import { useTheme } from '../theme/ThemeContext';
import { useToast } from '../context/ToastContext';
import { Spinner, Overlay } from '../components/primitives';

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
          fontFamily: mono ? 'monospace' : undefined,
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
  const config = {
    connected:            { color: theme.success, label: 'Connected',            bg: `${theme.success}18` },
    error:                { color: theme.error,   label: 'Connection Error',      bg: `${theme.error}18` },
    scan_timeout:         { color: theme.warning, label: 'Scan Timeout',          bg: `${theme.warning}18` },
    circuit_breaker_open: { color: theme.warning, label: 'Circuit Breaker Open',  bg: `${theme.warning}18` },
    scanning:             { color: theme.accent,  label: 'Scanning…',             bg: `${theme.accent}18` },
  };
  const c = config[status] ?? { color: theme.textMuted, label: 'Unknown', bg: theme.surfaceRaised };

  return (
    <div style={{ display: 'inline-flex', alignItems: 'center', gap: 7, padding: '6px 12px', borderRadius: 8, backgroundColor: c.bg, border: `1px solid ${c.color}33` }}>
      <div style={{ width: 8, height: 8, borderRadius: '50%', backgroundColor: c.color, flexShrink: 0 }} />
      <span style={{ fontSize: 13, fontWeight: 700, color: c.color }}>{c.label}</span>
    </div>
  );
}

export default function AccountSettingsScreen({ account, onBack, onConnectAccount, onAccountUpdated, onAccountDeleted }) {
  const { theme } = useTheme();
  const { toast } = useToast();

  const [label, setLabel]             = useState(account?.label ?? '');
  const [accessKeyId, setAccessKeyId] = useState(account?.access_key_id ?? '');
  const [secretKey, setSecretKey]     = useState('');
  const [region, setRegion]           = useState(account?.region ?? 'eu-central-1');
  const [scanIntervalHours, setScanIntervalHours] = useState(account?.scan_interval_hours?.toString() ?? '24');
  const [loading, setLoading]         = useState(false);
  const [scanning, setScanning]       = useState(false);
  const [deleting, setDeleting]       = useState(false);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [error, setError]             = useState('');

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

  async function handleScan() {
    setScanning(true);
    try {
      await scanAccount(account.id);
      toast('Scan started — results will appear shortly', 'info');
      setTimeout(() => { onAccountUpdated(account); setScanning(false); }, 5000);
    } catch {
      toast('Scan failed. Please try again.', 'error');
      setScanning(false);
    }
  }

  async function confirmDelete() {
    setShowDeleteConfirm(false);
    setDeleting(true);
    try {
      await deleteAccount(account.id);
      toast('Account deleted', 'success');
      onAccountDeleted(account.id);
    } catch {
      toast('Failed to delete account. Please try again.', 'error');
      setDeleting(false);
    }
  }

  const t = theme;
  const accountName = account.label || account.access_key_id.slice(0, 8) + '…';

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
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 10 }}>
              <StatusBadge status={account.status} theme={t} />
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
              <button
                onClick={() => setShowDeleteConfirm(true)}
                disabled={deleting}
                aria-label="Delete account"
                style={{
                  padding: '11px 16px',
                  borderRadius: 8,
                  backgroundColor: `${t.error}18`,
                  border: `1px solid ${t.error}40`,
                  cursor: deleting ? 'not-allowed' : 'pointer',
                  opacity: deleting ? 0.6 : 1,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  gap: 6,
                }}
              >
                {deleting ? <Spinner size={16} color={t.error} /> : (
                  <span style={{ color: t.error, fontSize: 14, fontWeight: 700 }}>Delete</span>
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

      {/* Delete confirmation modal */}
      <Overlay visible={showDeleteConfirm} onClose={() => setShowDeleteConfirm(false)}>
        <div
          role="dialog"
          aria-modal="true"
          aria-label="Confirm account deletion"
          onClick={e => e.stopPropagation()}
          style={{ backgroundColor: t.surface, borderRadius: 16, padding: 24, maxWidth: 400, width: '90vw', boxShadow: '0 16px 40px rgba(0,0,0,0.3)' }}
        >
          <span style={{ fontSize: 18, fontWeight: 800, color: t.text, display: 'block', marginBottom: 10 }}>
            Delete Account
          </span>
          <span style={{ fontSize: 14, color: t.textMid, lineHeight: '21px', display: 'block', marginBottom: 24 }}>
            Are you sure you want to delete <strong>"{accountName}"</strong>? All scan history and resource data for this account will be permanently removed.
          </span>
          <div style={{ display: 'flex', gap: 10 }}>
            <button
              onClick={() => setShowDeleteConfirm(false)}
              style={{ flex: 1, padding: '12px', borderRadius: 10, border: `1px solid ${t.border}`, backgroundColor: t.surfaceRaised, cursor: 'pointer' }}
            >
              <span style={{ color: t.text, fontSize: 14, fontWeight: 600 }}>Cancel</span>
            </button>
            <button
              onClick={confirmDelete}
              style={{ flex: 1, padding: '12px', borderRadius: 10, backgroundColor: t.error, border: 'none', cursor: 'pointer' }}
            >
              <span style={{ color: '#fff', fontSize: 14, fontWeight: 700 }}>Delete</span>
            </button>
          </div>
        </div>
      </Overlay>
    </div>
  );
}
