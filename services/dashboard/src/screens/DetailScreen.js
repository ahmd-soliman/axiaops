import React from 'react';
import {
  View,
  Text,
  ScrollView,
  TouchableOpacity,
  StyleSheet,
  Modal,
  TextInput,
  Alert,
  ActivityIndicator,
} from 'react-native';
import { serviceConfig } from '../components/serviceConfig';
import { useTheme } from '../theme/ThemeContext';
import { dismissGhost, revokeDismissal } from '../api/client';

// Reason codes exposed to the user.
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

export default function DetailScreen({ ghost, onBack, onDismissed }) {
  const { theme } = useTheme();
  const styles = createStyles(theme);
  const cfg = serviceConfig(ghost.service);

  // ── Dismiss / Snooze modal state ──────────────────────────────────────────
  const [modalVisible, setModalVisible] = React.useState(false);
  const [modalAction, setModalAction]   = React.useState('dismiss'); // 'dismiss' | 'snooze'
  const [selectedReason, setSelectedReason] = React.useState('intentional');
  const [note, setNote]       = React.useState('');
  const [snoozeDays, setSnoozeDays] = React.useState(7);
  const [submitting, setSubmitting] = React.useState(false);

  // Derive current dismissal state from ghost annotation.
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
      Alert.alert('Note required', 'Please add a note when selecting "Other".');
      return;
    }
    setSubmitting(true);
    try {
      const snoozeUntil = modalAction === 'snooze'
        ? new Date(Date.now() + snoozeDays * 24 * 60 * 60 * 1000).toISOString()
        : undefined;

      await dismissGhost({
        accountId:   ghost.internal_account_id,
        provider:    ghost.provider,
        service:     ghost.service,
        region:      ghost.region,
        resourceId:  ghost.resource_id,
        action:      modalAction,
        reason:      selectedReason,
        note:        note.trim(),
        snoozeUntil,
      });
      setModalVisible(false);
      if (onDismissed) onDismissed();
      onBack();
    } catch (err) {
      const msg = err.message === 'already_dismissed'
        ? 'This resource is already dismissed. Restore it first.'
        : 'Something went wrong. Please try again.';
      Alert.alert('Error', msg);
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
      Alert.alert('Error', 'Could not restore this resource. Please try again.');
    }
  }

  const stats = [
    { label: 'Monthly Cost', value: `${ghost.currency} ${ghost.monthly_cost.toFixed(2)}`, accent: true },
    { label: ghost.usage_metric, value: `${ghost.usage_avg} ${ghost.usage_unit}` },
    { label: 'Region', value: ghost.region },
    { label: 'Environment', value: ghost.tags?.env ?? '—' },
  ];

  const details = [
    { label: 'Provider',    value: ghost.provider },
    { label: 'Account ID',  value: ghost.account_id },
    { label: 'Owner',       value: ghost.owner },
    { label: 'Period',      value: `${fmtDate(ghost.period_start)} → ${fmtDate(ghost.period_end)}` },
    { label: 'Resource ID', value: ghost.resource_id, mono: true },
  ];

  return (
    <>
    <ScrollView style={styles.container} contentContainerStyle={styles.content}>

      {/* Dark header */}
      <View style={styles.header}>
        <TouchableOpacity onPress={onBack} style={styles.back}>
          <Text style={styles.backText}>← Back</Text>
        </TouchableOpacity>
        <View style={styles.headerBody}>
          <View style={styles.headerTop}>
            <View style={[styles.badge, { backgroundColor: cfg.color }]}>
              <Text style={styles.badgeText}>{cfg.label}</Text>
            </View>
            {ghost.is_ghost && (
              <View style={styles.ghostBadge}>
                <Text style={styles.ghostBadgeText}>zombie</Text>
              </View>
            )}
            {isDismissed && (
              <View style={[styles.ghostBadge, { backgroundColor: '#374151' }]}>
                <Text style={[styles.ghostBadgeText, { color: '#9CA3AF' }]}>dismissed</Text>
              </View>
            )}
            {isSnoozed && (
              <View style={[styles.ghostBadge, { backgroundColor: '#1e3a5f' }]}>
                <Text style={[styles.ghostBadgeText, { color: '#60a5fa' }]}>snoozed</Text>
              </View>
            )}
            <Text style={styles.headerService}>{ghost.service}</Text>
          </View>
          <Text style={styles.headerCost}>{ghost.currency} {ghost.monthly_cost.toFixed(2)}</Text>
          <Text style={styles.headerSub}>{ghost.is_ghost ? 'wasted per month' : 'per month'}</Text>

          {/* Dismiss / Snooze / Restore actions (only for ghost resources) */}
          {ghost.is_ghost && (
            <View style={styles.actionRow}>
              {ghost.dismissal_id ? (
                <TouchableOpacity style={[styles.actionBtn, styles.actionBtnRestore]} onPress={handleRestore}>
                  <Text style={styles.actionBtnText}>↩ Restore</Text>
                </TouchableOpacity>
              ) : (
                <>
                  <TouchableOpacity style={[styles.actionBtn, styles.actionBtnDismiss]} onPress={() => openModal('dismiss')}>
                    <Text style={styles.actionBtnText}>✕ Dismiss</Text>
                  </TouchableOpacity>
                  <TouchableOpacity style={[styles.actionBtn, styles.actionBtnSnooze]} onPress={() => openModal('snooze')}>
                    <Text style={styles.actionBtnText}>⏰ Snooze</Text>
                  </TouchableOpacity>
                </>
              )}
            </View>
          )}
        </View>
      </View>

      {/* Stats grid */}
      <View style={styles.statsGrid}>
        {stats.map(({ label, value, accent }) => (
          <View key={label} style={[styles.statCard, accent && styles.statCardAccent]}>
            <Text style={[styles.statValue, accent && styles.statValueAccent]}>{value}</Text>
            <Text style={styles.statLabel}>{label}</Text>
          </View>
        ))}
      </View>

      {/* Why flagged — only for ghost resources */}
      {ghost.is_ghost && (
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Why it was flagged</Text>
          <View style={[styles.reasonBox, { borderLeftColor: cfg.color }]}>
            <Text style={styles.reasonText}>{ghost.reason}</Text>
          </View>
        </View>
      )}

      {/* Resource details */}
      <View style={styles.section}>
        <Text style={styles.sectionTitle}>Resource Details</Text>
        <View style={styles.table}>
          {details.map(({ label, value, mono }, i) => (
            <View key={label} style={[styles.row, i === details.length - 1 && styles.rowLast]}>
              <Text style={styles.rowLabel}>{label}</Text>
              <Text style={[styles.rowValue, mono && styles.mono]} numberOfLines={2}>{value}</Text>
            </View>
          ))}
        </View>
      </View>

      {/* Suggested action — only for ghost resources */}
      {ghost.is_ghost && (
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Suggested Action</Text>
          <View style={styles.actionBox}>
            <Text style={styles.actionIcon}>⚡</Text>
            <Text style={styles.actionText}>{remediationHint(ghost.service, ghost.resource_id)}</Text>
          </View>
        </View>
      )}

    </ScrollView>

      {/* ── Dismiss / Snooze Modal ──────────────────────────────────────── */}
      <Modal visible={modalVisible} transparent animationType="slide" onRequestClose={() => setModalVisible(false)}>
        <View style={styles.modalOverlay}>
          <View style={styles.modalSheet}>
            <Text style={styles.modalTitle}>
              {modalAction === 'dismiss' ? 'Dismiss Resource' : 'Snooze Resource'}
            </Text>
            <Text style={styles.modalSub}>
              {modalAction === 'dismiss'
                ? 'This resource will be hidden from the ghost list.'
                : 'This resource will be hidden temporarily.'}
            </Text>

            {/* Reason selector */}
            <Text style={styles.modalLabel}>Reason</Text>
            {DISMISS_REASONS.map(r => (
              <TouchableOpacity
                key={r.value}
                style={[styles.reasonRow, selectedReason === r.value && styles.reasonRowActive]}
                onPress={() => setSelectedReason(r.value)}
              >
                <View style={[styles.radioCircle, selectedReason === r.value && styles.radioCircleActive]} />
                <Text style={[styles.reasonLabel, selectedReason === r.value && styles.reasonLabelActive]}>
                  {r.label}
                </Text>
              </TouchableOpacity>
            ))}

            {/* Note input (required for 'other') */}
            {(selectedReason === 'other' || note.length > 0) && (
              <TextInput
                style={styles.noteInput}
                placeholder={selectedReason === 'other' ? 'Note (required)…' : 'Add a note (optional)…'}
                placeholderTextColor="#6B7280"
                value={note}
                onChangeText={setNote}
                multiline
                numberOfLines={2}
              />
            )}

            {/* Snooze duration picker */}
            {modalAction === 'snooze' && (
              <>
                <Text style={styles.modalLabel}>Snooze for</Text>
                <View style={styles.snoozeRow}>
                  {SNOOZE_OPTIONS.map(o => (
                    <TouchableOpacity
                      key={o.days}
                      style={[styles.snoozeChip, snoozeDays === o.days && styles.snoozeChipActive]}
                      onPress={() => setSnoozeDays(o.days)}
                    >
                      <Text style={[styles.snoozeChipText, snoozeDays === o.days && styles.snoozeChipTextActive]}>
                        {o.label}
                      </Text>
                    </TouchableOpacity>
                  ))}
                </View>
              </>
            )}

            {/* Buttons */}
            <View style={styles.modalButtons}>
              <TouchableOpacity style={styles.cancelBtn} onPress={() => setModalVisible(false)}>
                <Text style={styles.cancelBtnText}>Cancel</Text>
              </TouchableOpacity>
              <TouchableOpacity
                style={[styles.confirmBtn, submitting && { opacity: 0.6 }]}
                onPress={handleSubmit}
                disabled={submitting}
              >
                {submitting
                  ? <ActivityIndicator size="small" color="#fff" />
                  : <Text style={styles.confirmBtnText}>
                      {modalAction === 'dismiss' ? 'Dismiss' : 'Snooze'}
                    </Text>
                }
              </TouchableOpacity>
            </View>
          </View>
        </View>
      </Modal>
    </>
  );
}

function remediationHint(service, resourceId = '') {
  if (service === 'AmazonVPC') {
    if (resourceId.startsWith('eipalloc-')) {
      return 'Release this Elastic IP in the EC2 console under Network & Security → Elastic IPs. This stops the $0.005/hour idle charge immediately.';
    }
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

function fmtDate(iso) {
  return new Date(iso).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
}

const createStyles = (theme) => StyleSheet.create({
  container: { flex: 1, backgroundColor: theme.bg },
  content: { paddingBottom: 48 },

  header: { backgroundColor: theme.surfaceAlt, paddingBottom: 28, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: theme.border },
  back: { paddingHorizontal: 20, paddingTop: 16, paddingBottom: 12 },
  backText: { color: theme.textMuted, fontWeight: '600', fontSize: 14 },
  headerBody: { paddingHorizontal: 20 },
  headerTop: { flexDirection: 'row', alignItems: 'center', gap: 10, marginBottom: 12 },
  badge: { paddingHorizontal: 9, paddingVertical: 4, borderRadius: 6 },
  badgeText: { color: '#FFFFFF', fontSize: 12, fontWeight: '800' },
  ghostBadge: { backgroundColor: theme.ghostBadgeBg, paddingHorizontal: 6, paddingVertical: 2, borderRadius: 4 },
  ghostBadgeText: { fontSize: 10, fontWeight: '700', color: theme.ghostBadgeText },
  headerService: { color: theme.textMuted, fontSize: 14, flex: 1 },
  headerCost: { color: theme.accent, fontSize: 42, fontWeight: '800', letterSpacing: -1 },
  headerSub: { color: theme.textMid, fontSize: 13, marginTop: 2 },

  statsGrid: { flexDirection: 'row', flexWrap: 'wrap', gap: 10, padding: 16 },
  statCard: {
    backgroundColor: theme.card,
    borderRadius: 10,
    padding: 14,
    flex: 1,
    minWidth: '45%',
    boxShadow: '0px 1px 4px rgba(0,0,0,0.04)',
  },
  statCardAccent: { backgroundColor: theme.accentLight },
  statValue: { fontSize: 17, fontWeight: '700', color: theme.text, marginBottom: 4 },
  statValueAccent: { color: theme.accent },
  statLabel: { fontSize: 11, color: theme.textMuted, fontWeight: '500', textTransform: 'uppercase', letterSpacing: 0.5 },

  section: { paddingHorizontal: 16, marginBottom: 16 },
  sectionTitle: {
    fontSize: 11, fontWeight: '700', color: theme.textMuted,
    letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 8,
  },

  reasonBox: { backgroundColor: theme.card, borderLeftWidth: 4, borderRadius: 8, padding: 16 },
  reasonText: { fontSize: 14, color: theme.textMid, lineHeight: 21 },

  table: { backgroundColor: theme.card, borderRadius: 10, overflow: 'hidden' },
  row: {
    flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center',
    paddingHorizontal: 16, paddingVertical: 13,
    borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: theme.border,
  },
  rowLast: { borderBottomWidth: 0 },
  rowLabel: { fontSize: 13, color: theme.textMuted },
  rowValue: { fontSize: 13, color: theme.text, fontWeight: '600', textAlign: 'right', flex: 1, marginLeft: 16 },
  mono: { fontFamily: 'monospace', fontSize: 11 },

  actionBox: {
    backgroundColor: theme.accentLight, borderRadius: 10, padding: 16,
    flexDirection: 'row', alignItems: 'flex-start', gap: 12,
    borderWidth: 1, borderColor: theme.accentBorder,
  },
  actionIcon: { fontSize: 18 },
  actionText: { fontSize: 14, color: theme.accentText, lineHeight: 21, flex: 1 },

  // ── Dismiss / Snooze action row in header ──────────────────────────────────
  actionRow: { flexDirection: 'row', gap: 10, marginTop: 18 },
  actionBtn: { paddingHorizontal: 16, paddingVertical: 8, borderRadius: 8, alignItems: 'center' },
  actionBtnDismiss: { backgroundColor: '#374151' },
  actionBtnSnooze:  { backgroundColor: '#1e3a5f' },
  actionBtnRestore: { backgroundColor: '#14532d' },
  actionBtnText:    { color: '#f3f4f6', fontWeight: '700', fontSize: 13 },

  // ── Modal ──────────────────────────────────────────────────────────────────
  modalOverlay: {
    flex: 1, justifyContent: 'flex-end',
    backgroundColor: 'rgba(0,0,0,0.55)',
  },
  modalSheet: {
    backgroundColor: theme.surface,
    borderTopLeftRadius: 20, borderTopRightRadius: 20,
    padding: 24, paddingBottom: 36,
  },
  modalTitle: { fontSize: 18, fontWeight: '800', color: theme.text, marginBottom: 4 },
  modalSub:   { fontSize: 13, color: theme.textMuted, marginBottom: 18 },
  modalLabel: { fontSize: 11, fontWeight: '700', color: theme.textMuted, letterSpacing: 1, textTransform: 'uppercase', marginBottom: 8, marginTop: 4 },

  reasonRow: {
    flexDirection: 'row', alignItems: 'center', gap: 12,
    paddingVertical: 10, paddingHorizontal: 12,
    borderRadius: 8, marginBottom: 4,
    borderWidth: 1, borderColor: theme.border,
  },
  reasonRowActive: { borderColor: theme.accent, backgroundColor: theme.accentLight },
  radioCircle: { width: 16, height: 16, borderRadius: 8, borderWidth: 2, borderColor: theme.textMuted },
  radioCircleActive: { borderColor: theme.accent, backgroundColor: theme.accent },
  reasonLabel: { fontSize: 14, color: theme.textMid },
  reasonLabelActive: { color: theme.accent, fontWeight: '600' },

  noteInput: {
    marginTop: 10,
    backgroundColor: theme.card,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: theme.border,
    padding: 12,
    color: theme.text,
    fontSize: 14,
    minHeight: 60,
  },

  snoozeRow: { flexDirection: 'row', gap: 8, marginBottom: 8, flexWrap: 'wrap' },
  snoozeChip: {
    paddingHorizontal: 14, paddingVertical: 7,
    borderRadius: 20, borderWidth: 1, borderColor: theme.border,
    backgroundColor: theme.surfaceRaised,
  },
  snoozeChipActive: { borderColor: theme.accent, backgroundColor: theme.accentLight },
  snoozeChipText: { fontSize: 13, color: theme.textMid, fontWeight: '600' },
  snoozeChipTextActive: { color: theme.accent },

  modalButtons: { flexDirection: 'row', gap: 12, marginTop: 20 },
  cancelBtn:  { flex: 1, paddingVertical: 13, borderRadius: 10, alignItems: 'center', borderWidth: 1, borderColor: theme.border },
  cancelBtnText: { color: theme.textMid, fontWeight: '700', fontSize: 15 },
  confirmBtn: { flex: 1, paddingVertical: 13, borderRadius: 10, alignItems: 'center', backgroundColor: theme.accent },
  confirmBtnText: { color: '#fff', fontWeight: '800', fontSize: 15 },
});
