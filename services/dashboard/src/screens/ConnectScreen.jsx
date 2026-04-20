import { useState } from 'react';
import { connectAccount, updateAccount } from '../api/client';
import { Spinner } from '../components/primitives';

// Local palette — kept in sync with darkTheme/lightTheme in ThemeContext.jsx.
// This screen uses the dark navy frame for chrome, light surfaces for cards.
const C = {
  bg: '#F6F8FB',
  navy: '#0B1220',
  navyMid: '#182031',
  accent: '#FB923C',        // orange-400 (dark-context brand)
  text: '#0F172A',
  textMid: '#475569',
  textMuted: '#94A3B8',
  white: '#FFFFFF',
  border: '#E2E8F0',
  error: '#DC2626',
};

const styles = {
  container: { flex: 1, backgroundColor: C.navy, minHeight: '100vh' },
  content: { padding: 20, paddingBottom: 48 },
  header: { paddingTop: 20, paddingBottom: 20, display: 'flex', alignItems: 'center', justifyContent: 'center' },
  brand: { color: C.accent, fontSize: 24, fontWeight: 800, letterSpacing: -0.5 },
  card: { backgroundColor: C.white, borderRadius: 16, padding: 24, display: 'flex', flexDirection: 'column', gap: 16 },
  title: { fontSize: 20, fontWeight: 800, color: C.text },
  subtitle: { fontSize: 14, color: C.textMid, lineHeight: '21px' },
  infoBox: { backgroundColor: C.bg, borderRadius: 8, padding: 14, border: `1px solid ${C.border}` },
  infoText: { fontSize: 13, color: C.textMid, lineHeight: '22px' },
  infoMono: { fontFamily: 'monospace', fontSize: 12, color: C.text },
  field: { display: 'flex', flexDirection: 'column', gap: 6 },
  fieldLabel: { fontSize: 13, fontWeight: 600, color: C.textMid },
  input: { backgroundColor: C.bg, border: `1px solid ${C.border}`, borderRadius: 8, paddingLeft: 12, paddingRight: 12, paddingTop: 10, paddingBottom: 10, fontSize: 14, color: C.text, outline: 'none' },
  inputMono: { fontFamily: 'monospace', fontSize: 13 },
  fieldHint: { fontSize: 12, color: C.textMuted, fontStyle: 'italic', marginTop: 2 },
  error: { fontSize: 13, color: C.error, fontWeight: 500 },
  btn: { backgroundColor: C.accent, borderRadius: 10, paddingTop: 14, paddingBottom: 14, display: 'flex', alignItems: 'center', justifyContent: 'center', marginTop: 4, border: 'none', cursor: 'pointer', width: '100%' },
  btnDisabled: { opacity: 0.6 },
  btnText: { color: C.white, fontSize: 16, fontWeight: 700 },
  skipBtn: { display: 'flex', alignItems: 'center', justifyContent: 'center', paddingTop: 8, paddingBottom: 8, background: 'none', border: 'none', cursor: 'pointer', width: '100%' },
  skipText: { fontSize: 14, color: C.textMuted },
};

export default function ConnectScreen({ onConnected, onSkip, onCancel, account }) {
  const isEdit = !!account;
  const [label, setLabel]             = useState(account?.label ?? '');
  const [accessKeyId, setAccessKeyId] = useState(account?.access_key_id ?? '');
  const [secretKey, setSecretKey]     = useState('');
  const [region, setRegion]           = useState(account?.region ?? 'eu-central-1');
  const [scanIntervalHours, setScanIntervalHours] = useState(account?.scan_interval_hours?.toString() ?? '24');
  const [loading, setLoading]         = useState(false);
  const [error, setError]             = useState('');

  async function handleSubmit() {
    if (!isEdit && (!accessKeyId.trim() || !secretKey.trim())) {
      setError('Access Key ID and Secret Access Key are required.');
      return;
    }
    if (isEdit && !accessKeyId.trim()) {
      setError('Access Key ID is required.');
      return;
    }
    setError('');
    setLoading(true);
    try {
      let result;
      if (isEdit) {
        const scanInterval = parseInt(scanIntervalHours, 10);
        if (isNaN(scanInterval) || scanInterval < 0) {
          setError('Scan interval must be a number >= 0.');
          setLoading(false);
          return;
        }
        result = await updateAccount(account.id, {
          label: label.trim() || 'My AWS Account',
          accessKeyId: accessKeyId.trim(),
          secretKey: secretKey.trim() || undefined,
          region: region.trim() || 'eu-central-1',
          scan_interval_hours: scanInterval,
        });
      } else {
        result = await connectAccount({
          provider: 'aws',
          label: label.trim() || 'My AWS Account',
          accessKeyId: accessKeyId.trim(),
          secretKey: secretKey.trim(),
          region: region.trim() || 'eu-central-1',
        });
      }
      onConnected(result);
    } catch {
      setError(isEdit ? 'Failed to update. Check your credentials and try again.' : 'Failed to connect. Check your credentials and try again.');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div style={styles.container}>
      <div style={styles.content}>
        <div style={styles.header}>
          <span style={styles.brand}>AxiaOps</span>
        </div>

        <div style={styles.card}>
          <span style={styles.title}>{isEdit ? 'Edit AWS Account' : 'Connect AWS Account'}</span>
          <span style={styles.subtitle}>
            {isEdit
              ? 'Update credentials or settings. Leave the secret key blank to keep the existing one.'
              : 'Create a read-only IAM user in your AWS account and paste the credentials below.'}
          </span>

          <div style={styles.infoBox}>
            <span style={styles.infoText}>
              Minimum IAM permissions required:{'\n'}
              <span style={styles.infoMono}>ReadOnlyAccess</span> or{'\n'}
              <span style={styles.infoMono}>ce:GetCostAndUsage</span>{'\n'}
              <span style={styles.infoMono}>cloudwatch:GetMetricStatistics</span>{'\n'}
              <span style={styles.infoMono}>ec2:DescribeAddresses</span>
            </span>
          </div>

          <Field label="Label (optional)" value={label} onChange={setLabel} placeholder="e.g. Production" />
          <Field label="AWS Access Key ID" value={accessKeyId} onChange={setAccessKeyId} placeholder="AKIAIOSFODNN7EXAMPLE" mono />
          <Field label="AWS Secret Access Key" value={secretKey} onChange={setSecretKey} placeholder={isEdit ? 'Leave blank to keep existing' : 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY'} mono type="password" />
          <Field label="Region" value={region} onChange={setRegion} placeholder="eu-central-1" mono />
          {isEdit && (
            <Field
              label="Auto-scan interval (hours)"
              value={scanIntervalHours}
              onChange={setScanIntervalHours}
              placeholder="24"
              type="number"
              hint="0 = on-demand (always eligible per 60-min check), or enter hours between scans"
            />
          )}

          {error ? <span style={styles.error}>{error}</span> : null}

          <button
            style={{ ...styles.btn, ...(loading ? styles.btnDisabled : {}) }}
            onClick={handleSubmit}
            disabled={loading}
          >
            {loading ? <Spinner size={20} color={C.white} /> : <span style={styles.btnText}>{isEdit ? 'Save Changes' : 'Connect Account'}</span>}
          </button>

          {onSkip && (
            <button style={styles.skipBtn} onClick={onSkip}>
              <span style={styles.skipText}>Skip for now</span>
            </button>
          )}
          {onCancel && (
            <button style={styles.skipBtn} onClick={onCancel}>
              <span style={styles.skipText}>Cancel</span>
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

function Field({ label, value, onChange, placeholder, mono, type = 'text', hint }) {
  return (
    <div style={styles.field}>
      <span style={styles.fieldLabel}>{label}</span>
      <input
        style={{ ...styles.input, ...(mono ? styles.inputMono : {}) }}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        autoCapitalize="none"
        autoCorrect="off"
        type={type}
      />
      {hint && <span style={styles.fieldHint}>{hint}</span>}
    </div>
  );
}
