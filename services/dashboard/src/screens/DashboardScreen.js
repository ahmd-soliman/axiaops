import React from 'react';
import {
  View,
  Text,
  FlatList,
  ScrollView,
  TouchableOpacity,
  ActivityIndicator,
  StyleSheet,
  RefreshControl,
  Alert,
} from 'react-native';
import { useQuery } from '@tanstack/react-query';
import { fetchSummary, fetchResources, fetchTrend, scanAccount, deleteAccount, fetchDismissals } from '../api/client';
import { serviceConfig } from '../components/serviceConfig';
import AccountSelector from '../components/AccountSelector';
import { useTheme } from '../theme/ThemeContext';

// calculateNextScanTime returns a human-readable time until the next scheduled scan.
// account: { last_scanned_at, scan_interval_hours }
// Returns: 'On-demand' for interval=0, 'Now' if overdue, 'in Xh' if hours, 'in Xm' if minutes
function calculateNextScanTime(account) {
  if (!account || account.scan_interval_hours === null || account.scan_interval_hours === undefined) return null;

  // If scan_interval_hours is 0, account is always eligible for scheduled scan (triggered on every check)
  if (account.scan_interval_hours === 0) {
    return 'On-demand';
  }

  // If negative interval (should not happen), don't show
  if (account.scan_interval_hours < 0) return null;

  // If never scanned, show based on interval
  if (!account.last_scanned_at) {
    return `in ${account.scan_interval_hours}h`;
  }

  // Calculate time until next scan
  const lastScanned = new Date(account.last_scanned_at);
  const nextScan = new Date(lastScanned.getTime() + account.scan_interval_hours * 60 * 60 * 1000);
  const now = new Date();
  const diffMs = nextScan - now;

  if (diffMs <= 0) return 'Now'; // Overdue

  const diffHours = Math.floor(diffMs / (60 * 60 * 1000));
  if (diffHours > 0) return `in ${diffHours}h`;

  const diffMinutes = Math.ceil(diffMs / (60 * 1000));
  return `in ${diffMinutes}m`;
}

// SavingsSparkline renders a simple bar chart of ghost savings over time.
// snaps: array of { total_monthly_cost, snapshot_at }
function SavingsSparkline({ snaps, theme }) {
  if (!snaps || snaps.length < 2) return null;

  const W = 220, H = 36, BAR_W = 4, GAP = 2;
  const values = snaps.map(s => s.total_monthly_cost);
  const maxVal = Math.max(...values, 0.01);
  // Only show the last N bars that fit in W
  const maxBars = Math.floor(W / (BAR_W + GAP));
  const visible = values.slice(-maxBars);

  return (
    <View style={{ width: W, height: H, flexDirection: 'row', alignItems: 'flex-end', marginTop: 8, opacity: 0.85 }}>
      {visible.map((v, i) => {
        const barH = Math.max(3, Math.round((v / maxVal) * H));
        const isLast = i === visible.length - 1;
        return (
          <View
            key={i}
            style={{
              width: BAR_W,
              height: barH,
              backgroundColor: isLast ? theme.accent : 'rgba(249,115,22,0.45)',
              marginRight: i < visible.length - 1 ? GAP : 0,
              borderRadius: 1,
            }}
          />
        );
      })}
    </View>
  );
}

export default function DashboardScreen({ onShowTrend, onSelectGhost, onLogout, orgName, accounts = [], onConnectAccount, onEditAccount, onDeleteAccount, selectedAccount, onSelectAccount }) {
  const { theme, toggleTheme, isDark } = useTheme();
  const styles = createStyles(theme);
  const [filterSvc, setFilterSvc]           = React.useState(null);
  const [filterOwner, setFilterOwner]       = React.useState(null);
  const [ghostOnly, setGhostOnly]           = React.useState(true);
  const [showDismissed, setShowDismissed]   = React.useState(false);
  const [scanning, setScanning]             = React.useState(null); // account id being scanned

  const summary    = useQuery({ queryKey: ['summary', selectedAccount],    queryFn: () => fetchSummary(selectedAccount) });
  const resources  = useQuery({ queryKey: ['resources', selectedAccount],  queryFn: () => fetchResources(selectedAccount) });
  const trend      = useQuery({ queryKey: ['trend'],                       queryFn: () => fetchTrend(null) });
  const dismissals = useQuery({ queryKey: ['dismissals', selectedAccount], queryFn: () => fetchDismissals(selectedAccount) });

  const isLoading    = summary.isLoading    || resources.isLoading;
  const isError      = summary.isError      || resources.isError;
  const isRefreshing = summary.isFetching   || resources.isFetching;

  // Build a set of dismissed resource_ids for quick lookup.
  const dismissedSet = React.useMemo(() => {
    const set = new Set();
    (dismissals.data ?? []).forEach(d => set.add(d.resource_id));
    return set;
  }, [dismissals.data]);

  // Collect unique owners from all resources for the team filter.
  const owners = React.useMemo(() => {
    const set = new Set((resources.data ?? []).map(r => r.owner).filter(Boolean));
    return [...set].sort();
  }, [resources.data]);

  function refresh() {
    summary.refetch();
    resources.refetch();
    trend.refetch();
    dismissals.refetch();
  }

  async function handleScan(accountId) {
    setScanning(accountId);
    try {
      await scanAccount(accountId);
      // Poll briefly then refresh — ingestion runs async on the server.
      setTimeout(() => { refresh(); setScanning(null); }, 3000);
    } catch {
      setScanning(null);
    }
  }

  if (isLoading) {
    return (
      <View style={[styles.center, { backgroundColor: theme.bg }]}>
        <ActivityIndicator size="large" color={theme.accent} />
        <Text style={[styles.loadingText, { color: theme.textMid }]}>Analysing resources…</Text>
      </View>
    );
  }

  if (isError) {
    return (
      <View style={[styles.center, { backgroundColor: theme.bg }]}>
        <Text style={[styles.errorIcon, { color: theme.textMuted }]}>⚠</Text>
        <Text style={[styles.errorTitle, { color: theme.text }]}>Service unavailable</Text>
        <Text style={[styles.errorHint, { color: theme.textMid }]}>Make sure the ingestion service is running on localhost:8080</Text>
        <TouchableOpacity style={[styles.retryBtn, { backgroundColor: theme.accent }]} onPress={refresh}>
          <Text style={[styles.retryText, { color: theme.white }]}>Retry</Text>
        </TouchableOpacity>
      </View>
    );
  }

  const byService = Object.entries(summary.data.by_service ?? {}).sort(
    (a, b) => b[1].savings - a[1].savings
  );

  return (
    <FlatList
      style={styles.list}
      refreshControl={<RefreshControl refreshing={isRefreshing} onRefresh={refresh} tintColor={theme.accent} />}
      ListHeaderComponent={
        <View>
          {/* Navbar */}
          <View style={styles.navbar}>
            <Text style={styles.navBrand}>AxiaOps</Text>
            <View style={{ flex: 1 }} />
            {accounts.length > 0 && (
              <AccountSelector
                accounts={accounts}
                selectedAccount={selectedAccount}
                onSelectAccount={onSelectAccount}
                onConnectAccount={onConnectAccount}
                onEditAccount={onEditAccount}
                onScanAccount={handleScan}
                scanning={scanning}
              />
            )}
            <TouchableOpacity onPress={toggleTheme} style={styles.themeBtn}>
              <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke={theme.text} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                {isDark ? (
                  // Sun icon - shown in dark mode to switch to light
                  <>
                    <path d="M8 12a4 4 0 1 0 8 0a4 4 0 1 0 -8 0"></path>
                    <path d="M3 12h1m8 -9v1m8 8h1m-9 8v1m-6.4 -15.4l.7 .7m12.1 -.7l-.7 .7m0 11.4l.7 .7m-12.1 -.7l-.7 .7"></path>
                  </>
                ) : (
                  // Moon icon - shown in light mode to switch to dark
                  <path d="M12 3c.132 0 .263 0 .393 0a7.5 7.5 0 0 0 7.92 12.446a9 9 0 1 1 -8.313 -12.454z"></path>
                )}
              </svg>
            </TouchableOpacity>
            {orgName ? (
              <View style={styles.orgPill}>
                <Text style={styles.orgPillText}>{orgName}</Text>
              </View>
            ) : null}
            <TouchableOpacity onPress={onLogout} style={styles.logoutBtn}>
              <Text style={styles.logoutText}>Sign out</Text>
            </TouchableOpacity>
          </View>

          {/* Show connect prompt if no accounts */}
          {accounts.length === 0 && (
            <View style={styles.connectPrompt}>
              <Text style={styles.connectPromptText}>Connect your first AWS account to get started</Text>
              <TouchableOpacity style={styles.connectBtn} onPress={onConnectAccount}>
                <Text style={styles.connectBtnText}>+ Connect AWS Account</Text>
              </TouchableOpacity>
            </View>
          )}

          {/* Hero */}
          <View style={styles.hero}>
            <Text style={styles.heroEyebrow}>Potential Monthly Savings</Text>
            <Text style={styles.heroAmount}>
              {summary.data.currency}{' '}
              {summary.data.potential_monthly_savings.toFixed(2)}
            </Text>
            <Text style={styles.heroSub}>
              {summary.data.total_ghosts} zombie resource{summary.data.total_ghosts !== 1 ? 's' : ''} detected across your accounts
            </Text>

            {/* Savings trend sparkline */}
            <TouchableOpacity activeOpacity={0.8} onPress={() => onShowTrend && onShowTrend()}>
              <SavingsSparkline snaps={trend.data} theme={theme} />
              {trend.data && trend.data.length >= 2 && (
                <Text style={styles.sparklineLabel}>Savings trend ({trend.data.length} scans)</Text>
              )}
            </TouchableOpacity>

            {/* By-service pills */}
            <ScrollView
              horizontal
              showsHorizontalScrollIndicator={false}
              style={styles.pillsRow}
              contentContainerStyle={{ gap: 8, paddingRight: 4 }}
            >
              {byService.map(([svc, data]) => {
                const cfg = serviceConfig(svc);
                const active = filterSvc === svc;
                return (
                  <TouchableOpacity
                    key={svc}
                    style={[styles.pill, active && styles.pillActive]}
                    onPress={() => setFilterSvc(active ? null : svc)}
                    activeOpacity={0.75}
                  >
                    <View style={[styles.pillDot, { backgroundColor: cfg.color }]} />
                    <Text style={styles.pillLabel}>{cfg.label}</Text>
                    <Text style={styles.pillSavings}>
                      {summary.data.currency}{data.savings.toFixed(0)}
                    </Text>
                  </TouchableOpacity>
                );
              })}
            </ScrollView>

            {/* Owner / team filter pills — only shown when there are multiple owners */}
            {owners.length > 1 && (
              <ScrollView
                horizontal
                showsHorizontalScrollIndicator={false}
                style={{ marginTop: 10 }}
                contentContainerStyle={{ gap: 6, paddingRight: 4 }}
              >
                {owners.map(owner => {
                  const active = filterOwner === owner;
                  return (
                    <TouchableOpacity
                      key={owner}
                      style={[styles.ownerPill, active && styles.ownerPillActive]}
                      onPress={() => setFilterOwner(active ? null : owner)}
                      activeOpacity={0.75}
                    >
                      <Text style={[styles.ownerPillText, active && styles.ownerPillTextActive]}>
                        👤 {owner}
                      </Text>
                    </TouchableOpacity>
                  );
                })}
              </ScrollView>
            )}
          </View>

          {/* Ghost-only / All Resources / Dismissed toggle */}
          <View style={styles.toggleRow}>
            <TouchableOpacity
              style={[styles.toggleBtn, ghostOnly && !showDismissed && styles.toggleBtnActive]}
              onPress={() => { setGhostOnly(true); setShowDismissed(false); }}
            >
              <Text style={[styles.toggleBtnText, ghostOnly && !showDismissed && styles.toggleBtnTextActive]}>
                Ghost Resources
              </Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={[styles.toggleBtn, !ghostOnly && !showDismissed && styles.toggleBtnActive]}
              onPress={() => { setGhostOnly(false); setShowDismissed(false); }}
            >
              <Text style={[styles.toggleBtnText, !ghostOnly && !showDismissed && styles.toggleBtnTextActive]}>
                All Resources
              </Text>
            </TouchableOpacity>
            {(dismissals.data?.length ?? 0) > 0 && (
              <TouchableOpacity
                style={[styles.toggleBtn, showDismissed && styles.toggleBtnDismissed]}
                onPress={() => setShowDismissed(v => !v)}
              >
                <Text style={[styles.toggleBtnText, showDismissed && styles.toggleBtnTextActive]}>
                  Dismissed ({dismissals.data?.length ?? 0})
                </Text>
              </TouchableOpacity>
            )}
          </View>

          <Text style={styles.sectionTitle}>
            {showDismissed ? 'Dismissed Resources' : ghostOnly ? 'Ghost Resources' : 'All Resources'}
          </Text>
        </View>
      }
      data={(() => {
        if (showDismissed) return dismissals.data ?? [];
        let list = resources.data ?? [];
        if (ghostOnly) list = list.filter(r => r.is_ghost);
        // Exclude resources that have an active dismissal from the ghost/all views.
        list = list.filter(r => !dismissedSet.has(r.resource_id));
        if (filterSvc) list = list.filter(r => r.service === filterSvc);
        if (filterOwner) list = list.filter(r => r.owner === filterOwner);
        return list;
      })()}
      keyExtractor={(item) => showDismissed ? String(item.id) : item.resource_id}
      renderItem={({ item }) => {
        if (showDismissed) {
          // Render a dismissal record card.
          const cfg = serviceConfig(item.service);
          const reasonLabel = {
            intentional: 'Intentional', scheduled_deletion: 'Scheduled deletion',
            false_positive: 'False positive', cost_accepted: 'Cost accepted', other: 'Other',
          }[item.reason] ?? item.reason;
          const isSnoozed = item.action === 'snooze';
          return (
            <View style={[styles.card, { borderLeftColor: cfg.color, opacity: 0.75 }]}>
              <View style={styles.cardTop}>
                <View style={[styles.badge, { backgroundColor: cfg.bg }]}>
                  <Text style={[styles.badgeText, { color: cfg.color }]}>{cfg.label}</Text>
                </View>
                <View style={[styles.ghostBadge, { backgroundColor: isSnoozed ? '#1e3a5f' : '#374151' }]}>
                  <Text style={[styles.ghostBadgeText, { color: isSnoozed ? '#60a5fa' : '#9CA3AF' }]}>
                    {isSnoozed ? 'snoozed' : 'dismissed'}
                  </Text>
                </View>
                <View style={{ flex: 1 }} />
              </View>
              <Text style={styles.cardResource} numberOfLines={1}>{item.resource_id}</Text>
              <View style={styles.cardMeta}>
                <Chip label={item.region} styles={styles} />
                <Chip label={reasonLabel} styles={styles} />
              </View>
              {item.note ? <Text style={styles.cardReason}>"{item.note}"</Text> : null}
              {isSnoozed && item.snoozed_until && (
                <Text style={styles.cardUsage}>
                  Until {new Date(item.snoozed_until).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })}
                </Text>
              )}
            </View>
          );
        }

        // Normal resource / ghost card.
        const cfg = serviceConfig(item.service);
        const isProd = item.tags?.env === 'production';
        return (
          <TouchableOpacity
            style={[styles.card, { borderLeftColor: cfg.color }]}
            onPress={() => onSelectGhost(item)}
            activeOpacity={0.75}
          >
            {/* Top row */}
            <View style={styles.cardTop}>
              <View style={[styles.badge, { backgroundColor: cfg.bg }]}>
                <Text style={[styles.badgeText, { color: cfg.color }]}>{cfg.label}</Text>
              </View>
              {item.is_ghost && (
                <View style={styles.ghostBadge}>
                  <Text style={styles.ghostBadgeText}>zombie</Text>
                </View>
              )}
              <View style={{ flex: 1 }} />
              <Text style={styles.cardCost}>{item.currency} {item.monthly_cost.toFixed(2)}</Text>
            </View>

            {/* Resource ID */}
            <Text style={styles.cardResource} numberOfLines={1}>{item.resource_id}</Text>

            {/* Meta row */}
            <View style={styles.cardMeta}>
              <Chip label={item.region} styles={styles} />
              <Chip label={item.tags?.env ?? 'unknown'} variant={isProd ? 'prod' : 'stag'} styles={styles} />
              <Text style={styles.cardOwner}>👤 {item.owner}</Text>
            </View>

            {/* Reason (ghosts) or usage metric (active resources) */}
            {item.is_ghost
              ? <Text style={styles.cardReason}>{item.reason}</Text>
              : item.usage_metric
                ? <Text style={styles.cardUsage}>{item.usage_metric}: {item.usage_avg.toFixed(2)} {item.usage_unit}</Text>
                : null
            }
          </TouchableOpacity>
        );
      }}
      ItemSeparatorComponent={() => <View style={{ height: 8 }} />}
      contentContainerStyle={{ paddingBottom: 40 }}
    />
  );
}

function Chip({ label, variant, styles }) {
  return (
    <View style={[styles.chip, variant === 'prod' && styles.chipProd, variant === 'stag' && styles.chipStag]}>
      <Text style={[styles.chipText, variant === 'prod' && styles.chipTextProd, variant === 'stag' && styles.chipTextStag]}>
        {label}
      </Text>
    </View>
  );
}

const createStyles = (theme) => StyleSheet.create({
  list: { flex: 1, backgroundColor: theme.bg },
  center: { flex: 1, justifyContent: 'center', alignItems: 'center', padding: 32, backgroundColor: theme.bg },

  loadingText: { marginTop: 14, color: theme.textMuted, fontSize: 14 },

  errorIcon: { fontSize: 32, marginBottom: 12 },
  errorTitle: { fontSize: 17, fontWeight: '700', color: theme.text, marginBottom: 6 },
  errorHint: { fontSize: 13, color: theme.textMuted, textAlign: 'center', lineHeight: 20 },
  retryBtn: { marginTop: 20, backgroundColor: theme.accent, paddingHorizontal: 28, paddingVertical: 11, borderRadius: 8 },
  retryText: { color: theme.white, fontWeight: '700', fontSize: 14 },

  // Navbar
  navbar: {
    backgroundColor: theme.surface,
    paddingHorizontal: 20,
    paddingVertical: 15,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 10,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: theme.border,
  },
  navBrand: { color: theme.text, fontSize: 18, fontWeight: '800', letterSpacing: 0.3, flex: 1 },
  themeBtn: { paddingHorizontal: 8, paddingVertical: 4, marginRight: 8 },
  themeBtnText: { fontSize: 16 },
  orgPill: { backgroundColor: theme.surfaceRaised, paddingHorizontal: 10, paddingVertical: 4, borderRadius: 5, marginRight: 8 },
  orgPillText: { color: theme.textMid, fontSize: 12, fontWeight: '600' },
  logoutBtn: { paddingHorizontal: 10, paddingVertical: 4, borderRadius: 5, borderWidth: 1, borderColor: theme.border },
  logoutText: { color: theme.textMuted, fontSize: 12, fontWeight: '600' },

  // Connect prompt (when no accounts)
  connectPrompt: {
    backgroundColor: theme.surfaceAlt,
    paddingHorizontal: 16,
    paddingVertical: 16,
    alignItems: 'center',
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: theme.border,
  },
  connectPromptText: {
    color: theme.textSub,
    fontSize: 14,
    marginBottom: 12,
  },
  connectBtn: {
    borderWidth: 1,
    borderColor: theme.accent,
    borderRadius: 8,
    paddingHorizontal: 16,
    paddingVertical: 8,
    borderStyle: 'dashed',
  },
  connectBtnText: { color: theme.accent, fontSize: 13, fontWeight: '600' },

  // Hero
  hero: {
    backgroundColor: theme.surfaceAlt,
    paddingHorizontal: 20,
    paddingTop: 28,
    paddingBottom: 24,
  },
  heroEyebrow: { color: theme.textMuted, fontSize: 11, fontWeight: '600', letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 6 },
  heroAmount: { color: theme.accent, fontSize: 46, fontWeight: '800', letterSpacing: -1 },
  heroSub: { color: theme.textSub, fontSize: 13, marginTop: 4, marginBottom: 4 },
  sparklineLabel: { color: theme.textSub, fontSize: 10, marginTop: 4, marginBottom: 16 },

  pillsRow: { marginTop: 4 },
  pill: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    backgroundColor: theme.surfaceRaised,
    borderRadius: 20,
    paddingHorizontal: 12,
    paddingVertical: 6,
  },
  pillActive: { backgroundColor: theme.accent, borderColor: theme.accent },
  pillDot: { width: 6, height: 6, borderRadius: 3 },
  pillLabel: { color: theme.text, fontSize: 12, fontWeight: '700' },
  pillSavings: { color: theme.textMuted, fontSize: 12 },

  ownerPill: {
    backgroundColor: theme.surfaceRaised,
    borderRadius: 20,
    paddingHorizontal: 12,
    paddingVertical: 5,
    borderWidth: 1,
    borderColor: theme.border,
  },
  ownerPillActive: { backgroundColor: theme.navy, borderColor: theme.navy },
  ownerPillText: { fontSize: 12, fontWeight: '600', color: theme.textMid },
  ownerPillTextActive: { color: theme.textOnDark },

  // Section title
  sectionTitle: {
    fontSize: 11,
    fontWeight: '700',
    color: theme.textMuted,
    letterSpacing: 1.5,
    textTransform: 'uppercase',
    paddingHorizontal: 16,
    paddingTop: 20,
    paddingBottom: 10,
  },

  // Cards
  card: {
    backgroundColor: theme.card,
    marginHorizontal: 16,
    borderRadius: 10,
    padding: 16,
    borderLeftWidth: 4,
    boxShadow: '0px 2px 6px rgba(0,0,0,0.05)',
  },
  cardTop: { flexDirection: 'row', alignItems: 'center', gap: 8, marginBottom: 6 },
  badge: { paddingHorizontal: 7, paddingVertical: 3, borderRadius: 5 },
  badgeText: { fontSize: 11, fontWeight: '800' },
  cardCost: { fontSize: 14, fontWeight: '800', color: theme.accent },
  cardResource: { fontSize: 11, color: theme.textMuted, fontFamily: 'monospace', marginBottom: 10 },

  cardMeta: { flexDirection: 'row', alignItems: 'center', gap: 6, marginBottom: 10 },
  chip: { backgroundColor: theme.chipBg, paddingHorizontal: 8, paddingVertical: 3, borderRadius: 4 },
  chipProd: { backgroundColor: theme.chipProdBg },
  chipStag: { backgroundColor: theme.chipStagBg },
  chipText: { fontSize: 11, color: theme.chipText, fontWeight: '500' },
  chipTextProd: { color: theme.chipProdText },
  chipTextStag: { color: theme.chipStagText },
  cardOwner: { fontSize: 11, color: theme.textMuted, marginLeft: 'auto' },

  cardReason: { fontSize: 12, color: theme.textMid, fontStyle: 'italic', lineHeight: 18 },
  cardUsage: { fontSize: 12, color: theme.textMuted, lineHeight: 18 },

  // Ghost-only toggle
  toggleRow: {
    flexDirection: 'row',
    paddingHorizontal: 16,
    paddingTop: 16,
    gap: 8,
  },
  toggleBtn: {
    paddingHorizontal: 14,
    paddingVertical: 7,
    borderRadius: 20,
    backgroundColor: theme.surfaceRaised,
  },
  toggleBtnActive: { backgroundColor: theme.navy },
  toggleBtnDismissed: { backgroundColor: '#374151' },
  toggleBtnText: { fontSize: 13, fontWeight: '600', color: theme.textMid },
  toggleBtnTextActive: { color: theme.textOnDark },

  // Ghost badge on resource cards
  ghostBadge: {
    backgroundColor: theme.ghostBadgeBg,
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  ghostBadgeText: { fontSize: 10, fontWeight: '700', color: theme.ghostBadgeText },
});
