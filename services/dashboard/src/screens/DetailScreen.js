import React from 'react';
import {
  View,
  Text,
  ScrollView,
  TouchableOpacity,
  StyleSheet,
} from 'react-native';
import { serviceConfig } from '../components/serviceConfig';

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
};

export default function DetailScreen({ ghost, onBack }) {
  const cfg = serviceConfig(ghost.service);

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
            <Text style={styles.headerService}>{ghost.service}</Text>
          </View>
          <Text style={styles.headerCost}>{ghost.currency} {ghost.monthly_cost.toFixed(2)}</Text>
          <Text style={styles.headerSub}>{ghost.is_ghost ? 'wasted per month' : 'per month'}</Text>
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

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: C.bg },
  content: { paddingBottom: 48 },

  header: { backgroundColor: C.navyMid, paddingBottom: 28 },
  back: { paddingHorizontal: 20, paddingTop: 16, paddingBottom: 12 },
  backText: { color: C.textMuted, fontWeight: '600', fontSize: 14 },
  headerBody: { paddingHorizontal: 20 },
  headerTop: { flexDirection: 'row', alignItems: 'center', gap: 10, marginBottom: 12 },
  badge: { paddingHorizontal: 9, paddingVertical: 4, borderRadius: 6 },
  badgeText: { color: C.white, fontSize: 12, fontWeight: '800' },
  ghostBadge: { backgroundColor: '#FEF2F2', paddingHorizontal: 6, paddingVertical: 2, borderRadius: 4 },
  ghostBadgeText: { fontSize: 10, fontWeight: '700', color: '#B91C1C' },
  headerService: { color: C.textMuted, fontSize: 14, flex: 1 },
  headerCost: { color: C.accent, fontSize: 42, fontWeight: '800', letterSpacing: -1 },
  headerSub: { color: C.textMid, fontSize: 13, marginTop: 2 },

  statsGrid: { flexDirection: 'row', flexWrap: 'wrap', gap: 10, padding: 16 },
  statCard: {
    backgroundColor: C.white,
    borderRadius: 10,
    padding: 14,
    flex: 1,
    minWidth: '45%',
    shadowColor: '#000',
    shadowOpacity: 0.04,
    shadowRadius: 4,
    shadowOffset: { width: 0, height: 1 },
  },
  statCardAccent: { backgroundColor: '#FFF7ED' },
  statValue: { fontSize: 17, fontWeight: '700', color: C.text, marginBottom: 4 },
  statValueAccent: { color: C.accent },
  statLabel: { fontSize: 11, color: C.textMuted, fontWeight: '500', textTransform: 'uppercase', letterSpacing: 0.5 },

  section: { paddingHorizontal: 16, marginBottom: 16 },
  sectionTitle: {
    fontSize: 11, fontWeight: '700', color: C.textMuted,
    letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 8,
  },

  reasonBox: { backgroundColor: C.white, borderLeftWidth: 4, borderRadius: 8, padding: 16 },
  reasonText: { fontSize: 14, color: C.textMid, lineHeight: 21 },

  table: { backgroundColor: C.white, borderRadius: 10, overflow: 'hidden' },
  row: {
    flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center',
    paddingHorizontal: 16, paddingVertical: 13,
    borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: C.border,
  },
  rowLast: { borderBottomWidth: 0 },
  rowLabel: { fontSize: 13, color: C.textMuted },
  rowValue: { fontSize: 13, color: C.text, fontWeight: '600', textAlign: 'right', flex: 1, marginLeft: 16 },
  mono: { fontFamily: 'monospace', fontSize: 11 },

  actionBox: {
    backgroundColor: '#FFFBEB', borderRadius: 10, padding: 16,
    flexDirection: 'row', alignItems: 'flex-start', gap: 12,
    borderWidth: 1, borderColor: '#FDE68A',
  },
  actionIcon: { fontSize: 18 },
  actionText: { fontSize: 14, color: '#78350F', lineHeight: 21, flex: 1 },
});
