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
import { connectAccount } from '../api/client';

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
};

export default function ConnectScreen({ onConnected, onSkip }) {
  const [label, setLabel]           = useState('');
  const [accessKeyId, setAccessKeyId] = useState('');
  const [secretKey, setSecretKey]   = useState('');
  const [region, setRegion]         = useState('us-east-1');
  const [loading, setLoading]       = useState(false);
  const [error, setError]           = useState('');

  async function handleConnect() {
    if (!accessKeyId.trim() || !secretKey.trim()) {
      setError('Access Key ID and Secret Access Key are required.');
      return;
    }
    setError('');
    setLoading(true);
    try {
      const account = await connectAccount({
        provider: 'aws',
        label: label.trim() || 'My AWS Account',
        accessKeyId: accessKeyId.trim(),
        secretKey: secretKey.trim(),
        region: region.trim() || 'us-east-1',
      });
      onConnected(account);
    } catch (e) {
      setError('Failed to connect. Check your credentials and try again.');
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
        <Text style={styles.title}>Connect AWS Account</Text>
        <Text style={styles.subtitle}>
          Create a read-only IAM user in your AWS account and paste the credentials below.
        </Text>

        <View style={styles.infoBox}>
          <Text style={styles.infoText}>
            Minimum IAM permissions required:{'\n'}
            <Text style={styles.infoMono}>ReadOnlyAccess</Text> or{'\n'}
            <Text style={styles.infoMono}>ce:GetCostAndUsage</Text>{'\n'}
            <Text style={styles.infoMono}>cloudwatch:GetMetricStatistics</Text>{'\n'}
            <Text style={styles.infoMono}>ec2:DescribeAddresses</Text>
          </Text>
        </View>

        <Field label="Label (optional)" value={label} onChangeText={setLabel} placeholder="e.g. Production" />
        <Field label="AWS Access Key ID" value={accessKeyId} onChangeText={setAccessKeyId} placeholder="AKIAIOSFODNN7EXAMPLE" mono />
        <Field label="AWS Secret Access Key" value={secretKey} onChangeText={setSecretKey} placeholder="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" mono secureTextEntry />
        <Field label="Region" value={region} onChangeText={setRegion} placeholder="us-east-1" mono />

        {error ? <Text style={styles.error}>{error}</Text> : null}

        <TouchableOpacity
          style={[styles.btn, loading && styles.btnDisabled]}
          onPress={handleConnect}
          disabled={loading}
          activeOpacity={0.85}
        >
          {loading
            ? <ActivityIndicator color={C.white} />
            : <Text style={styles.btnText}>Connect Account</Text>
          }
        </TouchableOpacity>

        {onSkip ? (
          <TouchableOpacity onPress={onSkip} style={styles.skipBtn}>
            <Text style={styles.skipText}>Skip for now</Text>
          </TouchableOpacity>
        ) : null}
      </View>
    </ScrollView>
  );
}

function Field({ label, value, onChangeText, placeholder, mono, secureTextEntry }) {
  return (
    <View style={styles.field}>
      <Text style={styles.fieldLabel}>{label}</Text>
      <TextInput
        style={[styles.input, mono && styles.inputMono]}
        value={value}
        onChangeText={onChangeText}
        placeholder={placeholder}
        placeholderTextColor={C.textMuted}
        autoCapitalize="none"
        autoCorrect={false}
        secureTextEntry={secureTextEntry}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: C.navy },
  content: { padding: 20, paddingBottom: 48 },

  header: { paddingVertical: 20, alignItems: 'center' },
  brand: { color: C.accent, fontSize: 24, fontWeight: '800', letterSpacing: -0.5 },

  card: {
    backgroundColor: C.white,
    borderRadius: 16,
    padding: 24,
    gap: 16,
  },
  title: { fontSize: 20, fontWeight: '800', color: C.text },
  subtitle: { fontSize: 14, color: C.textMid, lineHeight: 21 },

  infoBox: {
    backgroundColor: C.bg,
    borderRadius: 8,
    padding: 14,
    borderWidth: 1,
    borderColor: C.border,
  },
  infoText: { fontSize: 13, color: C.textMid, lineHeight: 22 },
  infoMono: { fontFamily: 'monospace', fontSize: 12, color: C.text },

  field: { gap: 6 },
  fieldLabel: { fontSize: 13, fontWeight: '600', color: C.textMid },
  input: {
    backgroundColor: C.bg,
    borderWidth: 1,
    borderColor: C.border,
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 10,
    fontSize: 14,
    color: C.text,
  },
  inputMono: { fontFamily: 'monospace', fontSize: 13 },

  error: { fontSize: 13, color: C.error, fontWeight: '500' },

  btn: {
    backgroundColor: C.accent,
    borderRadius: 10,
    paddingVertical: 14,
    alignItems: 'center',
    marginTop: 4,
  },
  btnDisabled: { opacity: 0.6 },
  btnText: { color: C.white, fontSize: 16, fontWeight: '700' },

  skipBtn: { alignItems: 'center', paddingVertical: 8 },
  skipText: { fontSize: 14, color: C.textMuted },
});
