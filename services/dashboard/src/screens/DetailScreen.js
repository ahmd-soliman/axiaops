import React from 'react';
import {
  View,
  Text,
  ScrollView,
  TouchableOpacity,
  StyleSheet,
} from 'react-native';

export default function DetailScreen({ ghost, onBack }) {
  const rows = [
    { label: 'Provider',      value: ghost.provider },
    { label: 'Account ID',    value: ghost.account_id },
    { label: 'Region',        value: ghost.region },
    { label: 'Resource ID',   value: ghost.resource_id, mono: true },
    { label: 'Owner',         value: ghost.owner },
    { label: 'Environment',   value: ghost.tags?.env ?? '—' },
    { label: 'Period',        value: `${fmtDate(ghost.period_start)} → ${fmtDate(ghost.period_end)}` },
    { label: 'Monthly Cost',  value: `${ghost.currency} ${ghost.monthly_cost.toFixed(2)}` },
    { label: 'Usage Metric',  value: ghost.usage_metric },
    { label: 'Usage (avg)',   value: `${ghost.usage_avg} ${ghost.usage_unit}` },
  ];

  return (
    <ScrollView style={styles.container} contentContainerStyle={styles.content}>
      <TouchableOpacity style={styles.back} onPress={onBack}>
        <Text style={styles.backText}>← Back</Text>
      </TouchableOpacity>

      <View style={styles.header}>
        <Text style={styles.service}>{ghost.service}</Text>
        <Text style={styles.cost}>{ghost.currency} {ghost.monthly_cost.toFixed(2)}/mo</Text>
      </View>

      <View style={styles.reasonBox}>
        <Text style={styles.reasonLabel}>Why it was flagged</Text>
        <Text style={styles.reasonText}>{ghost.reason}</Text>
      </View>

      <View style={styles.table}>
        {rows.map(({ label, value, mono }) => (
          <View key={label} style={styles.row}>
            <Text style={styles.rowLabel}>{label}</Text>
            <Text style={[styles.rowValue, mono && styles.mono]} numberOfLines={2}>{value}</Text>
          </View>
        ))}
      </View>

      <View style={styles.actionBox}>
        <Text style={styles.actionTitle}>Suggested Action</Text>
        <Text style={styles.actionText}>{remediationHint(ghost.service)}</Text>
      </View>
    </ScrollView>
  );
}

function remediationHint(service) {
  const hints = {
    AmazonEC2: 'Stop or terminate the instance. If it is part of an Auto Scaling group, remove it from the group first.',
    AmazonRDS: 'Create a final snapshot, then delete the DB instance. Confirm with the owner before deleting.',
    AWSLambda: 'Delete the function. Check for any EventBridge rules or triggers pointing to it first.',
    AmazonElasticLoadBalancing: 'Delete the load balancer. Verify no DNS records point to it.',
    AmazonVPC: 'Delete the NAT Gateway and release the associated Elastic IP to stop charges.',
  };
  return hints[service] ?? 'Review with the resource owner before taking action.';
}

function fmtDate(iso) {
  return new Date(iso).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#F7FAFC' },
  content: { padding: 16, paddingBottom: 40 },

  back: { marginBottom: 16 },
  backText: { color: '#E53E3E', fontWeight: '600', fontSize: 15 },

  header: {
    backgroundColor: '#FFFFFF',
    borderRadius: 8,
    padding: 20,
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    marginBottom: 12,
  },
  service: { fontSize: 17, fontWeight: '700', color: '#2D3748', flex: 1 },
  cost: { fontSize: 20, fontWeight: '800', color: '#E53E3E' },

  reasonBox: {
    backgroundColor: '#FFF5F5',
    borderLeftWidth: 4,
    borderLeftColor: '#E53E3E',
    borderRadius: 6,
    padding: 16,
    marginBottom: 12,
  },
  reasonLabel: { fontSize: 11, fontWeight: '700', color: '#E53E3E', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 4 },
  reasonText: { fontSize: 14, color: '#742A2A' },

  table: { backgroundColor: '#FFFFFF', borderRadius: 8, overflow: 'hidden', marginBottom: 12 },
  row: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    paddingHorizontal: 16,
    paddingVertical: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: '#EDF2F7',
  },
  rowLabel: { fontSize: 13, color: '#718096', flex: 1 },
  rowValue: { fontSize: 13, color: '#2D3748', fontWeight: '500', flex: 2, textAlign: 'right' },
  mono: { fontFamily: 'monospace', fontSize: 11 },

  actionBox: {
    backgroundColor: '#FFFFF0',
    borderLeftWidth: 4,
    borderLeftColor: '#D69E2E',
    borderRadius: 6,
    padding: 16,
  },
  actionTitle: { fontSize: 11, fontWeight: '700', color: '#D69E2E', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 4 },
  actionText: { fontSize: 14, color: '#744210' },
});
