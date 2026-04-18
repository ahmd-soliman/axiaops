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

export default function AccountSettingsScreen({ account, onBack, onAccountUpdated, onAccountDeleted }) {
  const [label, setLabel] = useState(account?.label ?? '');
  const [accessKeyId, setAccessKeyId] = useState(account?.access_key_id ?? '');
  const [secretKey, setSecretKey] = useState('');
  const [region, setRegion] = useState(account?.region ?? 'us-east-1');
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
      setError('Scan interval must be a number >= 0.');
      return;
    }

    setError('');
    setLoading(true);
    try {
      const result = await updateAccount(account.id, {
        label: label.trim() || 'My AWS Account',
        accessKeyId: accessKeyId.trim(),
        secretKey: secretKey.trim() || undefined,
        region: region.trim() || 'us-east-1',
        scan_interval_hours: scanInterval,
      });
      onAccountUpdated(result);
    } catch (e) {
      setError('Failed to update. Check your credentials and try again.');
    } finally {
      setLoading(false);
    }
  }

  async function handleScan() {
    setScanning(true);
    try {
      await scanAccount(account.id);
      // Brief delay then callback to refresh data
      setTimeout(() => {
        onAccountUpdated(account);
        setScanning(false);
      }, 3000);
    } catch (e) {
      setError('Scan failed. Please try again.');
      setScanning(false);
    }
  }

  function handleDelete() {
    setShowDeleteConfirm(true);
  }

  async function confirmDelete() {
    setShowDeleteConfirm(false);
    setDeleting(true);
    try {
      await deleteAccount(account.id);
      onAccountDeleted(account.id);
    } catch (e) {
      setError('Failed to delete account. Please try again.');
      setDeleting(false);
    }
  }

  const statusColor = account.status === 'error' ? C.error : 
                     account.status === 'scan_timeout' ? '#F59E0B' :
                     account.status === 'circuit_breaker_open' ? '#F59E0B' : C.success;

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

      <View style={styles.card}>
        {/* Account Status */}
        <View style={styles.statusSection}>
          <View style={styles.statusRow}>
            <View style={[styles.statusDot, { backgroundColor: statusColor }]} />
            <Text style={styles.statusText}>
              {account.status === 'connected' ? 'Connected' :
               account.status === 'error' ? 'Connection Error' :
               account.status === 'scan_timeout' ? 'Scan Timeout' :
               account.status === 'circuit_breaker_open' ? 'Circuit Breaker Open' : 'Unknown'}
            </Text>
          </View>
          {account.last_scanned_at && (
            <Text style={styles.lastScanned}>
              Last scanned: {new Date(account.last_scanned_at).toLocaleString()}
            </Text>
          )}
        </View>

        {/* Quick Actions */}
        <View style={styles.actionsSection}>
          <TouchableOpacity
            style={[styles.actionBtn, styles.scanBtn]}
            onPress={handleScan}
            disabled={scanning || account.status === 'circuit_breaker_open'}
          >
            {scanning ? (
              <ActivityIndicator size="small" color={C.white} />
            ) : (
              <Text style={styles.actionBtnText}>Scan Now</Text>
            )}
          </TouchableOpacity>
          
          <TouchableOpacity
            style={[styles.actionBtn, styles.deleteBtn]}
            onPress={handleDelete}
            disabled={deleting}
          >
            {deleting ? (
              <ActivityIndicator size="small" color={C.white} />
            ) : (
              <Text style={styles.actionBtnText}>Delete Account</Text>
            )}
          </TouchableOpacity>
        </View>

        {/* Settings Form */}
        <View style={styles.formSection}>
          <Text style={styles.sectionTitle}>Account Details</Text>
          
          <Field 
            label="Label" 
            value={label} 
            onChangeText={setLabel} 
            placeholder="e.g. Production AWS" 
          />
          
          <Field 
            label="AWS Access Key ID" 
            value={accessKeyId} 
            onChangeText={setAccessKeyId} 
            placeholder="AKIAIOSFODNN7EXAMPLE" 
            mono 
          />
          
          <Field 
            label="AWS Secret Access Key" 
            value={secretKey} 
            onChangeText={setSecretKey} 
            placeholder="Leave blank to keep existing" 
            mono 
            secureTextEntry 
          />
          
          <Field 
            label="Region" 
            value={region} 
            onChangeText={setRegion} 
            placeholder="us-east-1" 
            mono 
          />
          
          <Field
            label="Auto-scan interval (hours)"
            value={scanIntervalHours}
            onChangeText={setScanIntervalHours}
            placeholder="24"
            keyboardType="number-pad"
            hint="0 = on-demand only, or enter hours between automatic scans"
          />

          {error ? <Text style={styles.error}>{error}</Text> : null}

          <TouchableOpacity
            style={[styles.saveBtn, loading && styles.saveBtnDisabled]}
            onPress={handleSave}
            disabled={loading}
          >
            {loading ? (
              <ActivityIndicator color={C.white} />
            ) : (
              <Text style={styles.saveBtnText}>Save Changes</Text>
            )}
          </TouchableOpacity>
        </View>
      </View>

      {/* Delete Confirmation Modal */}
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
            <Text style={styles.modalTitle}>Delete Account</Text>
            <Text style={styles.modalMessage}>
              Are you sure you want to delete "{account.label || account.access_key_id.slice(0, 8) + '…'}"? This action cannot be undone.
            </Text>
            <View style={styles.modalButtons}>
              <TouchableOpacity
                style={[styles.modalBtn, styles.modalBtnCancel]}
                onPress={() => setShowDeleteConfirm(false)}
              >
                <Text style={styles.modalBtnTextCancel}>Cancel</Text>
              </TouchableOpacity>
              <TouchableOpacity
                style={[styles.modalBtn, styles.modalBtnDelete]}
                onPress={confirmDelete}
              >
                <Text style={styles.modalBtnTextDelete}>Delete</Text>
              </TouchableOpacity>
            </View>
          </View>
        </TouchableOpacity>
      </Modal>
    </ScrollView>
  );
}

function Field({ label, value, onChangeText, placeholder, mono, secureTextEntry, keyboardType, hint }) {
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
        keyboardType={keyboardType}
      />
      {hint && <Text style={styles.fieldHint}>{hint}</Text>}
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: C.navy },
  content: { paddingBottom: 48 },

  header: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 20,
    paddingVertical: 16,
    borderBottomWidth: 1,
    borderBottomColor: C.navyMid,
  },
  backBtn: { padding: 4 },
  backText: { color: C.accent, fontSize: 16, fontWeight: '600' },
  title: { color: C.white, fontSize: 18, fontWeight: '700', flex: 1, textAlign: 'center' },
  headerSpacer: { width: 60 },

  card: {
    backgroundColor: C.white,
    margin: 20,
    borderRadius: 16,
    padding: 24,
    gap: 24,
  },

  statusSection: { gap: 8 },
  statusRow: { flexDirection: 'row', alignItems: 'center', gap: 8 },
  statusDot: { width: 8, height: 8, borderRadius: 4 },
  statusText: { fontSize: 14, fontWeight: '600', color: C.text },
  lastScanned: { fontSize: 12, color: C.textMuted, marginLeft: 16 },

  actionsSection: { flexDirection: 'row', gap: 12 },
  actionBtn: {
    flex: 1,
    paddingVertical: 12,
    borderRadius: 8,
    alignItems: 'center',
  },
  scanBtn: { backgroundColor: C.accent },
  deleteBtn: { backgroundColor: C.error },
  actionBtnText: { color: C.white, fontSize: 14, fontWeight: '600' },

  formSection: { gap: 16 },
  sectionTitle: { fontSize: 16, fontWeight: '700', color: C.text },

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
  fieldHint: { fontSize: 12, color: C.textMuted, fontStyle: 'italic' },

  error: { fontSize: 13, color: C.error, fontWeight: '500' },

  saveBtn: {
    backgroundColor: C.accent,
    borderRadius: 10,
    paddingVertical: 14,
    alignItems: 'center',
    marginTop: 8,
  },
  saveBtnDisabled: { opacity: 0.6 },
  saveBtnText: { color: C.white, fontSize: 16, fontWeight: '700' },

  // Delete confirmation modal
  modalOverlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.5)',
    justifyContent: 'center',
    alignItems: 'center',
  },
  modalContent: {
    backgroundColor: C.white,
    borderRadius: 12,
    padding: 24,
    margin: 20,
    maxWidth: 400,
    width: '90%',
  },
  modalTitle: {
    fontSize: 18,
    fontWeight: '700',
    color: C.navy,
    marginBottom: 12,
  },
  modalMessage: {
    fontSize: 14,
    color: C.textMid,
    lineHeight: 20,
    marginBottom: 24,
  },
  modalButtons: {
    flexDirection: 'row',
    gap: 12,
  },
  modalBtn: {
    flex: 1,
    paddingVertical: 12,
    borderRadius: 8,
    alignItems: 'center',
  },
  modalBtnCancel: {
    backgroundColor: C.bg,
    borderWidth: 1,
    borderColor: C.border,
  },
  modalBtnDelete: {
    backgroundColor: C.error,
  },
  modalBtnTextCancel: {
    color: C.text,
    fontSize: 14,
    fontWeight: '600',
  },
  modalBtnTextDelete: {
    color: C.white,
    fontSize: 14,
    fontWeight: '600',
  },
});
