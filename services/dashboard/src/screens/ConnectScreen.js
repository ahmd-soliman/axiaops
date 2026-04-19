import React, { useState } from 'react';
import {
  View,
  Text,
  TextInput,
  TouchableOpacity,
  ActivityIndicator,
  ScrollView,
  StyleSheet,
} from 'react-native';
import { connectAccount, updateAccount } from '../api/client';
import { useTheme } from '../theme/ThemeContext';

// account prop — when provided the screen runs in edit mode (pre-filled, secret optional).
export default function ConnectScreen({ onConnected, onSkip, onCancel, account }) {
  const { theme } = useTheme();
  const styles = createStyles(theme);
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
      setError(isEdit
        ? 'Failed to update. Check your credentials and try again.'
        : 'Failed to connect. Check your credentials and try again.');
    } finally {
      setLoading(false);
    }
  }

  return (
    <ScrollView style={styles.container} contentContainerStyle={styles.content} keyboardShouldPersistTaps="handled">

      {/* Header */}
      <View style={styles.header}>
        <Text style={styles.brand}>AxiaOps</Text>
      </View>

      <View style={styles.card}>
        <Text style={styles.title}>{isEdit ? 'Edit AWS Account' : 'Connect AWS Account'}</Text>
        <Text style={styles.subtitle}>
          {isEdit
            ? 'Update credentials or settings. Leave the secret key blank to keep the existing one.'
            : 'Create a read-only IAM user in your AWS account and paste the credentials below.'}
        </Text>

        {/* IAM permissions info */}
        {!isEdit && (
          <View style={styles.infoBox}>
            <Text style={styles.infoTitle}>Required IAM permissions</Text>
            {[
              'ReadOnlyAccess (managed policy)',
              'ce:GetCostAndUsage',
              'cloudwatch:GetMetricStatistics',
              'ec2:DescribeAddresses',
            ].map((perm) => (
              <View key={perm} style={styles.infoRow}>
                <Text style={styles.infoBullet}>•</Text>
                <Text style={styles.infoMono}>{perm}</Text>
              </View>
            ))}
          </View>
        )}

        <Field label="Label (optional)" value={label} onChangeText={setLabel} placeholder="e.g. Production" theme={theme} styles={styles} />
        <Field label="AWS Access Key ID" value={accessKeyId} onChangeText={setAccessKeyId} placeholder="AKIAIOSFODNN7EXAMPLE" mono theme={theme} styles={styles} />
        <Field
          label="AWS Secret Access Key"
          value={secretKey}
          onChangeText={setSecretKey}
          placeholder={isEdit ? 'Leave blank to keep existing' : 'wJalrXUtnFEMI/K7MDENG/…'}
          mono
          secureTextEntry
          theme={theme}
          styles={styles}
        />
        <Field label="Default Region" value={region} onChangeText={setRegion} placeholder="eu-central-1" mono theme={theme} styles={styles} />
        {isEdit && (
          <Field
            label="Auto-scan interval (hours)"
            value={scanIntervalHours}
            onChangeText={setScanIntervalHours}
            placeholder="24"
            keyboardType="number-pad"
            hint="Set to 0 for on-demand only, or enter the number of hours between automatic scans."
            theme={theme}
            styles={styles}
          />
        )}

        {error ? (
          <View style={styles.errorBox}>
            <Text style={styles.errorText}>⚠ {error}</Text>
          </View>
        ) : null}

        <TouchableOpacity
          style={[styles.btn, loading && styles.btnDisabled]}
          onPress={handleSubmit}
          disabled={loading}
          activeOpacity={0.85}
        >
          {loading
            ? <ActivityIndicator color="#fff" />
            : <Text style={styles.btnText}>{isEdit ? 'Save Changes' : 'Connect Account'}</Text>
          }
        </TouchableOpacity>

        {onSkip ? (
          <TouchableOpacity onPress={onSkip} style={styles.secondaryBtn}>
            <Text style={styles.secondaryBtnText}>Skip for now</Text>
          </TouchableOpacity>
        ) : null}
        {onCancel ? (
          <TouchableOpacity onPress={onCancel} style={styles.secondaryBtn}>
            <Text style={styles.secondaryBtnText}>Cancel</Text>
          </TouchableOpacity>
        ) : null}
      </View>
    </ScrollView>
  );
}

function Field({ label, value, onChangeText, placeholder, mono, secureTextEntry, keyboardType, hint, theme, styles }) {
  return (
    <View style={styles.field}>
      <Text style={styles.fieldLabel}>{label}</Text>
      <TextInput
        style={[styles.input, mono && styles.inputMono]}
        value={value}
        onChangeText={onChangeText}
        placeholder={placeholder}
        placeholderTextColor={theme.textMuted}
        autoCapitalize="none"
        autoCorrect={false}
        secureTextEntry={secureTextEntry}
        keyboardType={keyboardType}
      />
      {hint ? <Text style={styles.fieldHint}>{hint}</Text> : null}
    </View>
  );
}

const createStyles = (theme) => StyleSheet.create({
  container: { flex: 1, backgroundColor: theme.bg },
  content: { padding: 20, paddingBottom: 48 },

  header: { paddingVertical: 20, alignItems: 'center' },
  brand: { color: theme.accent, fontSize: 24, fontWeight: '800', letterSpacing: -0.5 },

  card: {
    backgroundColor: theme.surface,
    borderRadius: 16,
    padding: 24,
    gap: 16,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: theme.border,
  },
  title: { fontSize: 20, fontWeight: '800', color: theme.text },
  subtitle: { fontSize: 14, color: theme.textMid, lineHeight: 21 },

  infoBox: {
    backgroundColor: theme.surfaceAlt,
    borderRadius: 10,
    padding: 14,
    borderWidth: 1,
    borderColor: theme.border,
    gap: 6,
  },
  infoTitle: { fontSize: 12, fontWeight: '700', color: theme.textMuted, textTransform: 'uppercase', letterSpacing: 0.8, marginBottom: 4 },
  infoRow: { flexDirection: 'row', alignItems: 'center', gap: 8 },
  infoBullet: { color: theme.accent, fontSize: 14, lineHeight: 20 },
  infoMono: { fontFamily: 'monospace', fontSize: 12, color: theme.textMid },

  field: { gap: 6 },
  fieldLabel: { fontSize: 13, fontWeight: '600', color: theme.textMid },
  input: {
    backgroundColor: theme.surfaceAlt,
    borderWidth: 1,
    borderColor: theme.border,
    borderRadius: 10,
    paddingHorizontal: 12,
    paddingVertical: 11,
    fontSize: 14,
    color: theme.text,
  },
  inputMono: { fontFamily: 'monospace', fontSize: 13 },
  fieldHint: { fontSize: 12, color: theme.textMuted, lineHeight: 18 },

  errorBox: {
    backgroundColor: '#FEF2F2',
    borderRadius: 8,
    padding: 12,
    borderWidth: 1,
    borderColor: '#FECACA',
  },
  errorText: { fontSize: 13, color: '#B91C1C', fontWeight: '500' },

  btn: {
    backgroundColor: theme.accent,
    borderRadius: 12,
    paddingVertical: 15,
    alignItems: 'center',
    marginTop: 4,
  },
  btnDisabled: { opacity: 0.6 },
  btnText: { color: '#fff', fontSize: 16, fontWeight: '700' },

  secondaryBtn: { alignItems: 'center', paddingVertical: 10 },
  secondaryBtnText: { fontSize: 14, color: theme.textMuted },
});
