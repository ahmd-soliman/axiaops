import { useState } from 'react';
import { connectAccount, updateAccount } from '../api/client';
import { useTheme } from '../theme/ThemeContext';
import { Spinner } from '../components/primitives';

function Field({ label, value, onChange, placeholder, mono, type = 'text', hint, theme }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <label style={{ fontSize: 13, fontWeight: 600, color: theme.textMid }}>
        {label}
      </label>
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

export default function ConnectScreen({ onConnected, onSkip, onCancel, account }) {
  const { theme, isDark } = useTheme();
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
          setError('Scan interval must be a number ≥ 0.');
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
    <div style={{ minHeight: '100%', backgroundColor: theme.bg }}>
      <div style={{ maxWidth: 520, margin: '0 auto', padding: '32px 20px 64px' }}>

        {/* Header */}
        <div style={{ marginBottom: 28 }}>
          <h1 style={{ fontSize: 22, fontWeight: 800, color: theme.text, margin: '0 0 6px' }}>
            {isEdit ? 'Edit AWS Account' : 'Connect AWS Account'}
          </h1>
          <p style={{ fontSize: 14, color: theme.textMid, lineHeight: '21px', margin: 0 }}>
            {isEdit
              ? 'Update credentials or settings. Leave the secret key blank to keep the existing one.'
              : 'Create a read-only IAM user in your AWS account and paste the credentials below.'}
          </p>
        </div>

        {/* IAM permissions info box */}
        {!isEdit && (
          <div style={{
            backgroundColor: isDark ? theme.surfaceRaised : '#EFF6FF',
            border: `1px solid ${isDark ? theme.border : '#BFDBFE'}`,
            borderRadius: 10,
            padding: '14px 16px',
            marginBottom: 24,
          }}>
            <span style={{ fontSize: 13, fontWeight: 700, color: isDark ? theme.textMid : '#1D4ED8', display: 'block', marginBottom: 6 }}>
              Required IAM permissions
            </span>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
              {['ReadOnlyAccess (or below)', 'ce:GetCostAndUsage', 'cloudwatch:GetMetricStatistics', 'ec2:DescribeAddresses'].map(p => (
                <code key={p} style={{ fontSize: 12, color: theme.textMid, fontFamily: 'monospace', backgroundColor: isDark ? 'rgba(255,255,255,0.06)' : 'rgba(0,0,0,0.05)', padding: '2px 6px', borderRadius: 4, display: 'inline-block', width: 'fit-content' }}>
                  {p}
                </code>
              ))}
            </div>
          </div>
        )}

        {/* Form card */}
        <div style={{
          backgroundColor: theme.surface,
          border: `1px solid ${theme.border}`,
          borderRadius: 16,
          padding: '24px',
          display: 'flex',
          flexDirection: 'column',
          gap: 18,
        }}>
          <Field label="Label (optional)" value={label} onChange={setLabel} placeholder="e.g. Production" theme={theme} />
          <Field label="AWS Access Key ID" value={accessKeyId} onChange={setAccessKeyId} placeholder="AKIAIOSFODNN7EXAMPLE" mono theme={theme} />
          <Field
            label="AWS Secret Access Key"
            value={secretKey}
            onChange={setSecretKey}
            placeholder={isEdit ? 'Leave blank to keep existing' : 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY'}
            mono
            type="password"
            theme={theme}
          />
          <Field label="Region" value={region} onChange={setRegion} placeholder="eu-central-1" mono theme={theme} />
          {isEdit && (
            <Field
              label="Auto-scan interval (hours)"
              value={scanIntervalHours}
              onChange={setScanIntervalHours}
              placeholder="24"
              type="number"
              hint="0 = on-demand only, or enter hours between automatic scans"
              theme={theme}
            />
          )}

          {error && (
            <div style={{ backgroundColor: `${theme.error}18`, border: `1px solid ${theme.error}40`, borderRadius: 8, padding: '10px 12px' }}>
              <span style={{ fontSize: 13, color: theme.error, fontWeight: 500 }}>{error}</span>
            </div>
          )}

          <button
            onClick={handleSubmit}
            disabled={loading}
            style={{
              backgroundColor: theme.accent,
              borderRadius: 10,
              padding: '14px',
              border: 'none',
              cursor: loading ? 'not-allowed' : 'pointer',
              opacity: loading ? 0.65 : 1,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              width: '100%',
              marginTop: 4,
            }}
          >
            {loading
              ? <Spinner size={20} color="#fff" />
              : <span style={{ color: '#fff', fontSize: 15, fontWeight: 700 }}>{isEdit ? 'Save Changes' : 'Connect Account'}</span>
            }
          </button>

          {onSkip && (
            <button onClick={onSkip} style={{ background: 'none', border: 'none', cursor: 'pointer', padding: '6px', textAlign: 'center', width: '100%' }}>
              <span style={{ fontSize: 14, color: theme.textMuted }}>Skip for now</span>
            </button>
          )}
          {onCancel && (
            <button onClick={onCancel} style={{ background: 'none', border: 'none', cursor: 'pointer', padding: '6px', textAlign: 'center', width: '100%' }}>
              <span style={{ fontSize: 14, color: theme.textMuted }}>Cancel</span>
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
