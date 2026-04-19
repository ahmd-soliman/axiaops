import React, { useState } from 'react';
import {
  View,
  Text,
  TextInput,
  TouchableOpacity,
  ActivityIndicator,
  ScrollView,
  StyleSheet,
  Modal,
} from 'react-native';
import { updateAccount, deleteAccount, scanAccount } from '../api/client';
import { useTheme } from '../theme/ThemeContext';

export default function AccountSettingsScreen({ account, onBack, onAccountUpdated, onAccountDeleted }) {
  const { theme } = useTheme();
  const styles = createStyles(theme);

  const [label, setLabel] = useState(account?.label ?? '');
  const [accessKeyId, setAccessKeyId] = useState(account?.access_key_id ?? '');
  const [secretKey, setSecretKey] = useState('');
  const [region, setRegion] = useState(account?.region ?? 'eu-central-1');
  const [scanIntervalHours, setScanIntervalHours] = useState(account?.scan_interval_hours?.toString() ?? '24');
  const [loading, setLoading] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [error, setError] = useState('');

  async function handleSave() {
    if (!accessKeyId.trim()) {
      setError('Access Key ID is required.');
      return;
    }
    const scanInterval = parseInt(scanIntervalHours, 10);
    if (isNaN(scanInterval) || scanInterval < 0) {
      setError('Scan interval must be a number ≥ 0.');
      return;
    }
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
      setTimeout(() => {
        onAccountUpdated(account);
        setScanning(false);
      }, 3000);
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

  const statusColor =
    account.status === 'error' ? theme.error :
    account.status === 'scan_timeout' || account.status === 'circuit_breaker_open' ? theme.warning :
    theme.success;

  const statusLabel =
    account.status === 'connected' ? 'Connected' :
    account.status === 'error' ? 'Connection Error' :
    account.status === 'scan_timeout' ? 'Scan Timeout' :
    account.status === 'circuit_breaker_open' ? 'Circuit Breaker Open' : 'Unknown';

  return (
    <ScrollView style={styles.container} contentContainerStyle={styles.content}>

      {/* Header */}
      <View style={styles.header}>
        <TouchableOpacity onPress={onBack} style={styles.backBtn}>
          <Text style={styles.backText}>← Back</Text>
        </TouchableOpacity>
        <Text style={styles.title}>Account Settings</Text>
        <View style={styles.headerSpacer} />
      </View>

      {/* Status card */}
      <View style={styles.statusCard}>
        <View style={styles.statusRow}>
          <View style={[styles.statusDot, { backgroundColor: statusColor }]} />
          <Text style={[styles.statusLabel, { color: statusColor }]}>{statusLabel}</Text>
        </View>
        {account.last_scanned_at && (
          <Text style={styles.lastScanned}>
            Last scanned {new Date(account.last_scanned_at).toLocaleString()}
          </Text>
        )}
        <TouchableOpacity
          style={[styles.scanBtn, (scanning || account.status === 'circuit_breaker_open') && styles.scanBtnDisabled]}
          onPress={handleScan}
          disabled={scanning || account.status === 'circuit_breaker_open'}
        >
          {scanning
            ? <ActivityIndicator size="small" color="#fff" />
            : <Text style={styles.scanBtnText}>Scan Now</Text>
          }
        </TouchableOpacity>
      </View>

      {/* Settings form */}
      <View style={styles.card}>
        <Text style={styles.sectionTitle}>Account Details</Text>

        <Field label="Label" value={label} onChangeText={setLabel} placeholder="e.g. Production AWS" theme={theme} styles={styles} />
        <Field label="AWS Access Key ID" value={accessKeyId} onChangeText={setAccessKeyId} placeholder="AKIAIOSFODNN7EXAMPLE" mono theme={theme} styles={styles} />
        <Field label="AWS Secret Access Key" value={secretKey} onChangeText={setSecretKey} placeholder="Leave blank to keep existing" mono secureTextEntry theme={theme} styles={styles} />
        <Field label="Region" value={region} onChangeText={setRegion} placeholder="eu-central-1" mono theme={theme} styles={styles} />
        <Field
          label="Auto-scan interval (hours)"
          value={scanIntervalHours}
          onChangeText={setScanIntervalHours}
          placeholder="24"
          keyboardType="number-pad"
          hint="0 = on-demand only. Enter hours between automatic scans."
          theme={theme}
          styles={styles}
        />

        {error ? (
          <View style={styles.errorBox}>
            <Text style={styles.errorText}>⚠ {error}</Text>
          </View>
        ) : null}

        <TouchableOpacity
          style={[styles.saveBtn, loading && styles.saveBtnDisabled]}
          onPress={handleSave}
          disabled={loading}
        >
          {loading
            ? <ActivityIndicator color="#fff" />
            : <Text style={styles.saveBtnText}>Save Changes</Text>
          }
        </TouchableOpacity>
      </View>

      {/* Danger zone — visually separated at the bottom */}
      <View style={styles.dangerZone}>
        <Text style={styles.dangerTitle}>Danger Zone</Text>
        <Text style={styles.dangerHint}>
          Deleting this account removes all associated scan data and cannot be undone.
        </Text>
        <TouchableOpacity
          style={[styles.deleteBtn, deleting && styles.deleteBtnDisabled]}
          onPress={() => setShowDeleteConfirm(true)}
          disabled={deleting}
        >
          {deleting
            ? <ActivityIndicator size="small" color={theme.error} />
            : <Text style={styles.deleteBtnText}>Delete Account</Text>
          }
        </TouchableOpacity>
      </View>

      {/* Delete confirmation modal */}
      <Modal
        visible={showDeleteConfirm}
        transparent
        animationType="fade"
        onRequestClose={() => setShowDeleteConfirm(false)}
      >
        <TouchableOpacity
          style={styles.modalOverlay}
          activeOpacity={1}
          onPress={() => setShowDeleteConfirm(false)}
        >
          <View style={styles.modalContent} onStartShouldSetResponder={() => true}>
            <Text style={styles.modalTitle}>Delete account?</Text>
            <Text style={styles.modalMessage}>
              "{account.label || account.access_key_id.slice(0, 8) + '…'}" and all its scan history will be permanently removed. This cannot be undone.
            </Text>
            <View style={styles.modalButtons}>
              <TouchableOpacity
                style={styles.modalBtnCancel}
                onPress={() => setShowDeleteConfirm(false)}
              >
                <Text style={styles.modalBtnCancelText}>Keep Account</Text>
              </TouchableOpacity>
              <TouchableOpacity style={styles.modalBtnDelete} onPress={confirmDelete}>
                <Text style={styles.modalBtnDeleteText}>Yes, Delete</Text>
              </TouchableOpacity>
            </View>
          </View>
        </TouchableOpacity>
      </Modal>
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
  content: { paddingBottom: 48 },

  header: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 20,
    paddingVertical: 16,
    backgroundColor: theme.surface,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: theme.border,
  },
  backBtn: { padding: 4, minWidth: 60 },
  backText: { color: theme.accent, fontSize: 15, fontWeight: '600' },
  title: { color: theme.text, fontSize: 17, fontWeight: '700', flex: 1, textAlign: 'center' },
  headerSpacer: { minWidth: 60 },

  // Status card
  statusCard: {
    backgroundColor: theme.surface,
    margin: 16,
    borderRadius: 14,
    padding: 18,
    gap: 10,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: theme.border,
  },
  statusRow: { flexDirection: 'row', alignItems: 'center', gap: 8 },
  statusDot: { width: 9, height: 9, borderRadius: 5 },
  statusLabel: { fontSize: 14, fontWeight: '700' },
  lastScanned: { fontSize: 12, color: theme.textMuted },
  scanBtn: {
    backgroundColor: theme.accent,
    borderRadius: 10,
    paddingVertical: 11,
    alignItems: 'center',
    marginTop: 4,
  },
  scanBtnDisabled: { opacity: 0.5 },
  scanBtnText: { color: '#fff', fontSize: 14, fontWeight: '700' },

  // Settings card
  card: {
    backgroundColor: theme.surface,
    marginHorizontal: 16,
    borderRadius: 14,
    padding: 20,
    gap: 16,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: theme.border,
  },
  sectionTitle: { fontSize: 15, fontWeight: '700', color: theme.text },

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

  saveBtn: {
    backgroundColor: theme.accent,
    borderRadius: 12,
    paddingVertical: 14,
    alignItems: 'center',
    marginTop: 4,
  },
  saveBtnDisabled: { opacity: 0.6 },
  saveBtnText: { color: '#fff', fontSize: 15, fontWeight: '700' },

  // Danger zone
  dangerZone: {
    margin: 16,
    marginTop: 24,
    borderRadius: 14,
    padding: 18,
    borderWidth: 1,
    borderColor: '#FECACA',
    backgroundColor: '#FFF5F5',
    gap: 10,
  },
  dangerTitle: { fontSize: 13, fontWeight: '700', color: '#B91C1C', textTransform: 'uppercase', letterSpacing: 0.8 },
  dangerHint: { fontSize: 13, color: '#7F1D1D', lineHeight: 19 },
  deleteBtn: {
    borderWidth: 1,
    borderColor: '#B91C1C',
    borderRadius: 10,
    paddingVertical: 11,
    alignItems: 'center',
  },
  deleteBtnDisabled: { opacity: 0.5 },
  deleteBtnText: { color: '#B91C1C', fontSize: 14, fontWeight: '700' },

  // Modal
  modalOverlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.55)',
    justifyContent: 'center',
    alignItems: 'center',
  },
  modalContent: {
    backgroundColor: theme.surface,
    borderRadius: 16,
    padding: 24,
    margin: 24,
    maxWidth: 400,
    width: '90%',
    gap: 12,
  },
  modalTitle: { fontSize: 18, fontWeight: '800', color: theme.text },
  modalMessage: { fontSize: 14, color: theme.textMid, lineHeight: 21 },
  modalButtons: { flexDirection: 'row', gap: 10, marginTop: 8 },
  modalBtnCancel: {
    flex: 1,
    paddingVertical: 13,
    borderRadius: 10,
    alignItems: 'center',
    borderWidth: 1,
    borderColor: theme.border,
    backgroundColor: theme.surfaceAlt,
  },
  modalBtnCancelText: { color: theme.textMid, fontSize: 14, fontWeight: '700' },
  modalBtnDelete: {
    flex: 1,
    paddingVertical: 13,
    borderRadius: 10,
    alignItems: 'center',
    backgroundColor: '#B91C1C',
  },
  modalBtnDeleteText: { color: '#fff', fontSize: 14, fontWeight: '700' },
});
