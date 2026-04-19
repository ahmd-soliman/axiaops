import { useState } from 'react';
import { updateAccount, deleteAccount, scanAccount } from '../api/client';
import { Spinner } from '../components/primitives';
import { Overlay } from '../components/primitives';

const C = {
  bg: '#F8FAFC',
  navy: '#0F172A',
  navyMid: '#1E293B',
  accent: '#F97316',
  text: '#0F172A',
  textMid: '#475569',
  textMuted: '#94A3B8',
  white: '#FFFFFF',
  border: '#E2E8F0',
  error: '#B91C1C',
  success: '#059669',
};

const s = {
  container: { flex: 1, backgroundColor: C.navy, minHeight: '100vh' },
  content: { paddingBottom: 48 },
  header: { display: 'flex', flexDirection: 'row', alignItems: 'center', paddingLeft: 20, paddingRight: 20, paddingTop: 16, paddingBottom: 16, borderBottom: `1px solid ${C.navyMid}` },
  backBtn: { padding: 4, background: 'none', border: 'none', cursor: 'pointer' },
  backText: { color: C.accent, fontSize: 16, fontWeight: 600 },
  title: { color: C.white, fontSize: 18, fontWeight: 700, flex: 1, textAlign: 'center' },
  headerSpacer: { width: 60 },
  card: { backgroundColor: C.white, margin: 20, borderRadius: 16, padding: 24, display: 'flex', flexDirection: 'column', gap: 24 },
  statusSection: { display: 'flex', flexDirection: 'column', gap: 8 },
  statusRow: { display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 8 },
  statusDot: { width: 8, height: 8, borderRadius: '50%' },
  statusText: { fontSize: 14, fontWeight: 600, color: C.text },
  lastScanned: { fontSize: 12, color: C.textMuted, marginLeft: 16 },
  actionsSection: { display: 'flex', flexDirection: 'row', gap: 12 },
  actionBtn: { flex: 1, paddingTop: 12, paddingBottom: 12, borderRadius: 8, display: 'flex', alignItems: 'center', justifyContent: 'center', border: 'none', cursor: 'pointer' },
  actionBtnText: { color: C.white, fontSize: 14, fontWeight: 600 },
  formSection: { display: 'flex', flexDirection: 'column', gap: 16 },
  sectionTitle: { fontSize: 16, fontWeight: 700, color: C.text },
  field: { display: 'flex', flexDirection: 'column', gap: 6 },
  fieldLabel: { fontSize: 13, fontWeight: 600, color: C.textMid },
  input: { backgroundColor: C.bg, border: `1px solid ${C.border}`, borderRadius: 8, paddingLeft: 12, paddingRight: 12, paddingTop: 10, paddingBottom: 10, fontSize: 14, color: C.text, outline: 'none' },
  inputMono: { fontFamily: 'monospace', fontSize: 13 },
  fieldHint: { fontSize: 12, color: C.textMuted, fontStyle: 'italic' },
  error: { fontSize: 13, color: C.error, fontWeight: 500 },
  saveBtn: { backgroundColor: C.accent, borderRadius: 10, paddingTop: 14, paddingBottom: 14, display: 'flex', alignItems: 'center', justifyContent: 'center', marginTop: 8, border: 'none', cursor: 'pointer', width: '100%' },
  saveBtnText: { color: C.white, fontSize: 16, fontWeight: 700 },
  // Modal
  modalContent: { backgroundColor: C.white, borderRadius: 12, padding: 24, margin: 20, maxWidth: 400, width: '90%' },
  modalTitle: { fontSize: 18, fontWeight: 700, color: C.navy, marginBottom: 12 },
  modalMessage: { fontSize: 14, color: C.textMid, lineHeight: '20px', marginBottom: 24 },
  modalButtons: { display: 'flex', flexDirection: 'row', gap: 12 },
  modalBtn: { flex: 1, paddingTop: 12, paddingBottom: 12, borderRadius: 8, display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer', border: 'none' },
};

export default function AccountSettingsScreen({ account, onBack, onAccountUpdated, onAccountDeleted }) {
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
    if (isNaN(scanInterval) || scanInterval < 0) { setError('Scan interval must be a number >= 0.'); return; }
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
      setTimeout(() => { onAccountUpdated(account); setScanning(false); }, 3000);
    } catch {
      setError('Scan failed. Please try again.');
      setScanning(false);
    }
  }

  async function confirmDelete() {
    setShowDeleteConfirm(false);
    setDeleting(true);
    try {
      await deleteAccount(account.id);
      onAccountDeleted(account.id);
    } catch {
      setError('Failed to delete account. Please try again.');
      setDeleting(false);
    }
  }

  const statusColor = account.status === 'error' ? C.error
    : account.status === 'scan_timeout' || account.status === 'circuit_breaker_open' ? '#F59E0B'
    : C.success;

  return (
    <div style={s.container}>
      <div style={s.content}>
        <div style={s.header}>
          <button style={s.backBtn} onClick={onBack}>
            <span style={s.backText}>← Back</span>
          </button>
          <span style={s.title}>Account Settings</span>
          <div style={s.headerSpacer} />
        </div>

        <div style={s.card}>
          {/* Status */}
          <div style={s.statusSection}>
            <div style={s.statusRow}>
              <div style={{ ...s.statusDot, backgroundColor: statusColor }} />
              <span style={s.statusText}>
                {account.status === 'connected' ? 'Connected'
                  : account.status === 'error' ? 'Connection Error'
                  : account.status === 'scan_timeout' ? 'Scan Timeout'
                  : account.status === 'circuit_breaker_open' ? 'Circuit Breaker Open'
                  : 'Unknown'}
              </span>
            </div>
            {account.last_scanned_at && (
              <span style={s.lastScanned}>Last scanned: {new Date(account.last_scanned_at).toLocaleString()}</span>
            )}
          </div>

          {/* Quick actions */}
          <div style={s.actionsSection}>
            <button
              style={{ ...s.actionBtn, backgroundColor: C.accent, opacity: scanning || account.status === 'circuit_breaker_open' ? 0.6 : 1 }}
              onClick={handleScan}
              disabled={scanning || account.status === 'circuit_breaker_open'}
            >
              {scanning ? <Spinner size={18} color={C.white} /> : <span style={s.actionBtnText}>Scan Now</span>}
            </button>
            <button
              style={{ ...s.actionBtn, backgroundColor: C.error, opacity: deleting ? 0.6 : 1 }}
              onClick={() => setShowDeleteConfirm(true)}
              disabled={deleting}
            >
              {deleting ? <Spinner size={18} color={C.white} /> : <span style={s.actionBtnText}>Delete Account</span>}
            </button>
          </div>

          {/* Form */}
          <div style={s.formSection}>
            <span style={s.sectionTitle}>Account Details</span>
            <Field label="Label" value={label} onChange={setLabel} placeholder="e.g. Production AWS" />
            <Field label="AWS Access Key ID" value={accessKeyId} onChange={setAccessKeyId} placeholder="AKIAIOSFODNN7EXAMPLE" mono />
            <Field label="AWS Secret Access Key" value={secretKey} onChange={setSecretKey} placeholder="Leave blank to keep existing" mono type="password" />
            <Field label="Region" value={region} onChange={setRegion} placeholder="eu-central-1" mono />
            <Field label="Auto-scan interval (hours)" value={scanIntervalHours} onChange={setScanIntervalHours} placeholder="24" type="number" hint="0 = on-demand only, or enter hours between automatic scans" />

            {error ? <span style={s.error}>{error}</span> : null}

            <button style={{ ...s.saveBtn, opacity: loading ? 0.6 : 1 }} onClick={handleSave} disabled={loading}>
              {loading ? <Spinner size={20} color={C.white} /> : <span style={s.saveBtnText}>Save Changes</span>}
            </button>
          </div>
        </div>
      </div>

      {/* Delete confirmation */}
      <Overlay visible={showDeleteConfirm} onClose={() => setShowDeleteConfirm(false)}>
        <div style={s.modalContent}>
          <span style={{ ...s.modalTitle, display: 'block' }}>Delete Account</span>
          <span style={{ ...s.modalMessage, display: 'block' }}>
            Are you sure you want to delete "{account.label || account.access_key_id.slice(0, 8) + '…'}"? This action cannot be undone.
          </span>
          <div style={s.modalButtons}>
            <button style={{ ...s.modalBtn, backgroundColor: C.bg, border: `1px solid ${C.border}` }} onClick={() => setShowDeleteConfirm(false)}>
              <span style={{ color: C.text, fontSize: 14, fontWeight: 600 }}>Cancel</span>
            </button>
            <button style={{ ...s.modalBtn, backgroundColor: C.error }} onClick={confirmDelete}>
              <span style={{ color: C.white, fontSize: 14, fontWeight: 600 }}>Delete</span>
            </button>
          </div>
        </div>
      </Overlay>
    </div>
  );
}

function Field({ label, value, onChange, placeholder, mono, type = 'text', hint }) {
  return (
    <div style={s.field}>
      <span style={s.fieldLabel}>{label}</span>
      <input
        style={{ ...s.input, ...(mono ? s.inputMono : {}) }}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        autoCapitalize="none"
        autoCorrect="off"
        type={type}
      />
      {hint && <span style={s.fieldHint}>{hint}</span>}
    </div>
  );
}
