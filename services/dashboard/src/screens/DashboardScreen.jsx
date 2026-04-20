import { useState, useMemo } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { fetchSummary, fetchResources, fetchTrend, fetchDismissals, scanAccount, dismissGhost } from '../api/client';
import { serviceConfig } from '../components/serviceConfig';
import AccountSelector from '../components/AccountSelector';
import { useTheme } from '../theme/ThemeContext';
import { Spinner } from '../components/primitives';
import { useToast } from '../context/ToastContext';

// ─── Constants ────────────────────────────────────────────────────────────────

const SORT_OPTIONS = [
  { value: 'cost_desc',  label: 'Cost: High → Low' },
  { value: 'cost_asc',   label: 'Cost: Low → High' },
  { value: 'service',    label: 'Service' },
  { value: 'region',     label: 'Region' },
];

const DISMISS_REASONS = [
  { value: 'intentional',        label: 'Intentionally idle' },
  { value: 'scheduled_deletion', label: 'Scheduled for deletion' },
  { value: 'false_positive',     label: 'False positive' },
  { value: 'cost_accepted',      label: 'Cost accepted' },
  { value: 'other',              label: 'Other (add note)' },
];

const SNOOZE_OPTIONS = [
  { label: '1 day',  days: 1 },
  { label: '7 days', days: 7 },
  { label: '30 days', days: 30 },
  { label: '90 days', days: 90 },
];

// ─── Sparkline ────────────────────────────────────────────────────────────────

function SavingsSparkline({ snaps, color }) {
  if (!snaps || snaps.length < 2) return null;
  const W = 80, H = 28, BAR_W = 3, GAP = 1;
  const values = snaps.map(s => s.total_monthly_cost);
  const maxVal = Math.max(...values, 0.01);
  const maxBars = Math.floor(W / (BAR_W + GAP));
  const visible = values.slice(-maxBars);
  return (
    <div style={{ display: 'flex', alignItems: 'flex-end', gap: GAP, width: W, height: H, marginTop: 6 }}>
      {visible.map((v, i) => {
        const barH = Math.max(2, Math.round((v / maxVal) * H));
        const isLast = i === visible.length - 1;
        return (
          <div
            key={i}
            style={{ width: BAR_W, height: barH, backgroundColor: isLast ? color : `${color}66`, borderRadius: 1, flexShrink: 0 }}
          />
        );
      })}
    </div>
  );
}

// ─── Summary cards ────────────────────────────────────────────────────────────

function SummaryCards({ summary, trend, onShowTrend, theme }) {
  const data = summary.data;
  const byService = Object.entries(data?.by_service ?? {})
    .sort((a, b) => b[1].savings - a[1].savings)
    .slice(0, 3);

  const latestSnap = trend.data?.[trend.data.length - 1];
  const prevSnap   = trend.data?.[trend.data.length - 2];
  const delta = latestSnap && prevSnap
    ? ((latestSnap.total_monthly_cost - prevSnap.total_monthly_cost) / Math.max(prevSnap.total_monthly_cost, 0.01)) * 100
    : null;

  const cardStyle = {
    backgroundColor: theme.surface,
    border: `1px solid ${theme.border}`,
    borderRadius: 12,
    padding: '16px 18px',
    flex: 1,
    minWidth: 0,
  };

  return (
    <div style={{ display: 'flex', gap: 12, padding: '16px 16px 0', flexWrap: 'wrap' }}>
      {/* Total Waste */}
      <button
        onClick={onShowTrend}
        aria-label="View savings trend"
        style={{ backgroundColor: theme.surface, border: `1px solid ${theme.border}`, borderRadius: 12, padding: '16px 18px', flex: 1, minWidth: 160, cursor: 'pointer', textAlign: 'left' }}
      >
        <span style={{ fontSize: 11, fontWeight: 600, color: theme.textMuted, letterSpacing: 1.2, textTransform: 'uppercase', display: 'block', marginBottom: 4 }}>
          Monthly Waste
        </span>
        <span style={{ fontSize: 26, fontWeight: 800, color: theme.accent, letterSpacing: -0.5, display: 'block' }}>
          {data?.currency} {(data?.potential_monthly_savings ?? 0).toFixed(2)}
        </span>
        {delta !== null && (
          <span style={{ fontSize: 12, color: delta > 0 ? theme.error : theme.success, fontWeight: 600, marginTop: 2, display: 'block' }}>
            {delta > 0 ? '▲' : '▼'} {Math.abs(delta).toFixed(1)}% vs last scan
          </span>
        )}
        <SavingsSparkline snaps={trend.data} color={theme.accent} />
      </button>

      {/* Ghost count */}
      <div style={{ ...cardStyle, minWidth: 140 }}>
        <span style={{ fontSize: 11, fontWeight: 600, color: theme.textMuted, letterSpacing: 1.2, textTransform: 'uppercase', display: 'block', marginBottom: 4 }}>
          Zombies
        </span>
        <span style={{ fontSize: 26, fontWeight: 800, color: theme.text, display: 'block' }}>
          {data?.total_ghosts ?? 0}
        </span>
        <span style={{ fontSize: 12, color: theme.textMuted, display: 'block', marginTop: 2 }}>
          {data?.total_ghosts === 1 ? 'resource' : 'resources'} wasting budget
        </span>
      </div>

      {/* Top offenders */}
      <div style={{ ...cardStyle, minWidth: 160 }}>
        <span style={{ fontSize: 11, fontWeight: 600, color: theme.textMuted, letterSpacing: 1.2, textTransform: 'uppercase', display: 'block', marginBottom: 8 }}>
          By Service
        </span>
        {byService.length > 0 ? byService.map(([svc, d]) => {
          const cfg = serviceConfig(svc);
          return (
            <div key={svc} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 5 }}>
              <div style={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: cfg.color, flexShrink: 0 }} />
              <span style={{ fontSize: 12, fontWeight: 600, color: theme.textMid, flex: 1 }}>{cfg.label}</span>
              <span style={{ fontSize: 12, color: theme.accent, fontWeight: 700 }}>
                {data?.currency}{d.savings.toFixed(0)}
              </span>
            </div>
          );
        }) : (
          <span style={{ fontSize: 13, color: theme.textMuted }}>No data yet</span>
        )}
      </div>
    </div>
  );
}

// ─── Search + Sort bar ────────────────────────────────────────────────────────

function FilterBar({ search, onSearch, sortBy, onSort, theme, activeFilters, onClearFilter }) {
  return (
    <div style={{ padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
      <div style={{ display: 'flex', gap: 8 }}>
        {/* Search */}
        <div style={{ flex: 1, position: 'relative' }}>
          <svg
            style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', pointerEvents: 'none' }}
            width="14" height="14" viewBox="0 0 24 24" fill="none" stroke={theme.textMuted} strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"
          >
            <circle cx="11" cy="11" r="8" />
            <path d="m21 21-4.35-4.35" />
          </svg>
          <input
            type="search"
            value={search}
            onChange={e => onSearch(e.target.value)}
            placeholder="Search resource ID, owner, region…"
            aria-label="Search resources"
            style={{
              width: '100%',
              paddingLeft: 32,
              paddingRight: 12,
              paddingTop: 8,
              paddingBottom: 8,
              backgroundColor: theme.surfaceRaised,
              border: `1px solid ${theme.border}`,
              borderRadius: 8,
              fontSize: 13,
              color: theme.text,
              outline: 'none',
              boxSizing: 'border-box',
            }}
          />
        </div>

        {/* Sort */}
        <select
          value={sortBy}
          onChange={e => onSort(e.target.value)}
          aria-label="Sort resources"
          style={{
            padding: '8px 10px',
            backgroundColor: theme.surfaceRaised,
            border: `1px solid ${theme.border}`,
            borderRadius: 8,
            fontSize: 13,
            color: theme.textMid,
            cursor: 'pointer',
            outline: 'none',
            flexShrink: 0,
          }}
        >
          {SORT_OPTIONS.map(o => (
            <option key={o.value} value={o.value}>{o.label}</option>
          ))}
        </select>
      </div>

      {/* Active filter chips */}
      {activeFilters.length > 0 && (
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          {activeFilters.map(f => (
            <button
              key={f.key}
              onClick={() => onClearFilter(f.key)}
              aria-label={`Remove ${f.label} filter`}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 5,
                padding: '3px 8px 3px 10px',
                backgroundColor: theme.accentLight,
                border: `1px solid ${theme.accentBorder}`,
                borderRadius: 20,
                cursor: 'pointer',
                fontSize: 12,
                color: theme.accentText,
                fontWeight: 600,
              }}
            >
              {f.label}
              <span style={{ fontSize: 14, lineHeight: 1, opacity: 0.7 }}>×</span>
            </button>
          ))}
          <button
            onClick={() => activeFilters.forEach(f => onClearFilter(f.key))}
            style={{ padding: '3px 8px', background: 'none', border: 'none', cursor: 'pointer', fontSize: 12, color: theme.textMuted }}
          >
            Clear all
          </button>
        </div>
      )}
    </div>
  );
}

// ─── Service + owner pill rows ────────────────────────────────────────────────

function FilterPills({ byService, owners, filterSvc, filterOwner, onFilterSvc, onFilterOwner, currency, theme, isDark }) {
  return (
    <div style={{ padding: '0 16px 12px' }}>
      {/* Service pills */}
      {byService.length > 0 && (
        <div
          role="group"
          aria-label="Filter by service"
          style={{ display: 'flex', gap: 6, overflowX: 'auto', paddingBottom: 6, marginBottom: 6 }}
        >
          {byService.map(([svc, data]) => {
            const cfg = serviceConfig(svc);
            const active = filterSvc === svc;
            return (
              <button
                key={svc}
                onClick={() => onFilterSvc(active ? null : svc)}
                aria-pressed={active}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 5,
                  backgroundColor: active ? theme.accent : theme.surfaceRaised,
                  borderRadius: 20,
                  padding: '5px 10px',
                  border: `1px solid ${active ? theme.accent : theme.border}`,
                  cursor: 'pointer',
                  flexShrink: 0,
                }}
              >
                <div style={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: active ? '#ffffff' : cfg.color }} />
                <span style={{ fontSize: 12, fontWeight: 700, color: active ? '#ffffff' : theme.text }}>{cfg.label}</span>
                <span style={{ fontSize: 11, color: active ? 'rgba(255,255,255,0.8)' : theme.textMuted }}>{currency}{data.savings.toFixed(0)}</span>
              </button>
            );
          })}
        </div>
      )}

      {/* Owner pills */}
      {owners.length > 1 && (
        <div
          role="group"
          aria-label="Filter by owner"
          style={{ display: 'flex', gap: 6, overflowX: 'auto', paddingBottom: 2 }}
        >
          {owners.map(owner => {
            const active = filterOwner === owner;
            return (
              <button
                key={owner}
                onClick={() => onFilterOwner(active ? null : owner)}
                aria-pressed={active}
                style={{
                  backgroundColor: active ? theme.navy : theme.surfaceRaised,
                  borderRadius: 20,
                  padding: '4px 10px',
                  border: `1px solid ${active ? theme.navy : theme.border}`,
                  cursor: 'pointer',
                  flexShrink: 0,
                }}
              >
                <span style={{ fontSize: 12, fontWeight: 600, color: active ? '#fff' : theme.textMid }}>
                  {owner}
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ─── Resource card ────────────────────────────────────────────────────────────

function ResourceCard({ item, onSelect, isSelected, onToggleSelect, theme, isDark }) {
  const cfg  = serviceConfig(item.service);
  const env  = item.tags?.env ?? 'unknown';
  const envVariant = ['prod', 'production'].includes(env) ? 'prod' : ['staging', 'stg'].includes(env) ? 'stag' : null;
  const envColor = envVariant === 'prod' ? theme.error : envVariant === 'stag' ? theme.warning : theme.textMuted;

  return (
    <div
      style={{
        backgroundColor: theme.card,
        marginLeft: 16,
        marginRight: 16,
        marginBottom: 8,
        borderRadius: 10,
        boxShadow: '0 1px 4px rgba(0,0,0,0.06)',
        display: 'flex',
        alignItems: 'stretch',
        overflow: 'hidden',
        border: isSelected ? `1px solid ${theme.accent}` : `1px solid ${theme.border}`,
        borderLeft: `4px solid ${cfg.color}`,
        transition: 'border-color 0.15s',
      }}
    >
      {/* Checkbox column */}
      <div
        style={{ padding: '16px 0 16px 12px', display: 'flex', alignItems: 'flex-start', flexShrink: 0 }}
        onClick={e => { e.stopPropagation(); onToggleSelect(item.resource_id); }}
      >
        <input
          type="checkbox"
          checked={isSelected}
          onChange={() => onToggleSelect(item.resource_id)}
          aria-label={`Select ${item.resource_id}`}
          style={{ width: 16, height: 16, cursor: 'pointer', accentColor: theme.accent, marginTop: 1 }}
        />
      </div>

      {/* Main content */}
      <button
        onClick={() => onSelect(item)}
        style={{
          flex: 1,
          padding: '12px 14px 12px 10px',
          background: 'none',
          border: 'none',
          cursor: 'pointer',
          textAlign: 'left',
          minWidth: 0,
        }}
      >
        {/* Row 1: badges + cost */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 5 }}>
          <div style={{
            padding: '2px 7px',
            borderRadius: 5,
            backgroundColor: isDark ? cfg.darkBg : cfg.bg,
            border: `1px solid ${cfg.color}33`,
            flexShrink: 0,
          }}>
            <span style={{ fontSize: 11, fontWeight: 800, color: cfg.color }}>{cfg.label}</span>
          </div>

          {item.is_ghost && (
            <div style={{
              padding: '2px 6px',
              borderRadius: 4,
              backgroundColor: theme.ghostBadgeBg,
              border: `1px solid ${theme.error}33`,
              flexShrink: 0,
            }}>
              <span style={{ fontSize: 10, fontWeight: 700, color: theme.ghostBadgeText, textTransform: 'uppercase', letterSpacing: 0.3 }}>
                zombie
              </span>
            </div>
          )}

          <div style={{ flex: 1 }} />

          <span style={{ fontSize: 15, fontWeight: 800, color: theme.accent, flexShrink: 0 }}>
            {item.currency} {item.monthly_cost.toFixed(2)}<span style={{ fontSize: 11, fontWeight: 500, color: theme.textMuted }}>/mo</span>
          </span>
        </div>

        {/* Row 2: resource ID */}
        <span style={{
          fontSize: 11,
          color: theme.textMuted,
          fontFamily: 'monospace',
          display: 'block',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          marginBottom: 7,
        }}>
          {item.resource_id}
        </span>

        {/* Row 3: metadata chips + owner */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
          <span style={{
            fontSize: 11,
            color: theme.textMid,
            backgroundColor: theme.surfaceRaised,
            border: `1px solid ${theme.border}`,
            padding: '2px 7px',
            borderRadius: 4,
          }}>
            {item.region}
          </span>
          <span style={{
            fontSize: 11,
            fontWeight: envVariant === 'prod' ? 700 : 500,
            color: envColor,
            backgroundColor: envVariant === 'prod' ? `${theme.error}18` : theme.surfaceRaised,
            border: `1px solid ${envVariant === 'prod' ? `${theme.error}33` : theme.border}`,
            padding: '2px 7px',
            borderRadius: 4,
          }}>
            {env}
          </span>
          {item.owner && (
            <span style={{ fontSize: 11, color: theme.textMuted, marginLeft: 'auto' }}>
              {item.owner}
            </span>
          )}
        </div>

        {/* Row 4: detection reason / usage */}
        {(item.is_ghost || item.usage_metric) && (
          <div style={{ marginTop: 6 }}>
            <span style={{ fontSize: 12, color: item.is_ghost ? theme.textMid : theme.textMuted, fontStyle: 'italic', lineHeight: '18px', display: 'block' }}>
              {item.is_ghost ? item.reason : `${item.usage_metric}: ${item.usage_avg?.toFixed(2)} ${item.usage_unit}`}
            </span>
          </div>
        )}
      </button>
    </div>
  );
}

// ─── Dismissed resource card ──────────────────────────────────────────────────

function DismissedCard({ item, theme, isDark }) {
  const cfg = serviceConfig(item.service);
  const reasonLabel = {
    intentional: 'Intentional', scheduled_deletion: 'Scheduled', false_positive: 'False positive',
    cost_accepted: 'Cost accepted', other: 'Other',
  }[item.reason] ?? item.reason;
  const isSnoozed = item.action === 'snooze';

  return (
    <div style={{
      backgroundColor: theme.card,
      marginLeft: 16,
      marginRight: 16,
      marginBottom: 8,
      borderRadius: 10,
      padding: '12px 14px',
      border: `1px solid ${theme.border}`,
      borderLeft: `4px solid ${cfg.color}`,
      opacity: 0.75,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 5 }}>
        <div style={{ padding: '2px 7px', borderRadius: 5, backgroundColor: isDark ? cfg.darkBg : cfg.bg }}>
          <span style={{ fontSize: 11, fontWeight: 800, color: cfg.color }}>{cfg.label}</span>
        </div>
        <div style={{
          padding: '2px 6px', borderRadius: 4,
          backgroundColor: isSnoozed ? (isDark ? '#1e3a5f' : '#DBEAFE') : (isDark ? '#374151' : '#F3F4F6'),
        }}>
          <span style={{ fontSize: 10, fontWeight: 700, color: isSnoozed ? '#60a5fa' : '#9CA3AF' }}>
            {isSnoozed ? 'snoozed' : 'dismissed'}
          </span>
        </div>
        <div style={{ flex: 1 }} />
        <span style={{ fontSize: 11, padding: '2px 7px', borderRadius: 4, backgroundColor: theme.surfaceRaised, border: `1px solid ${theme.border}`, color: theme.textMid, fontWeight: 600 }}>
          {reasonLabel}
        </span>
      </div>
      <span style={{ fontSize: 11, color: theme.textMuted, fontFamily: 'monospace', display: 'block', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', marginBottom: 4 }}>
        {item.resource_id}
      </span>
      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 11, color: theme.textMuted, backgroundColor: theme.surfaceRaised, border: `1px solid ${theme.border}`, padding: '2px 7px', borderRadius: 4 }}>
          {item.region}
        </span>
        {isSnoozed && item.snoozed_until && (
          <span style={{ fontSize: 11, color: theme.textMuted }}>
            until {new Date(item.snoozed_until).toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })}
          </span>
        )}
      </div>
      {item.note ? (
        <span style={{ fontSize: 12, color: theme.textMid, fontStyle: 'italic', display: 'block', marginTop: 4 }}>"{item.note}"</span>
      ) : null}
    </div>
  );
}

// ─── Bulk action bar ──────────────────────────────────────────────────────────

function BulkActionBar({ count, onDismiss, onSnooze, onExport, onClear, theme }) {
  return (
    <div
      role="toolbar"
      aria-label="Bulk actions"
      style={{
        position: 'fixed',
        bottom: 72,
        left: '50%',
        transform: 'translateX(-50%)',
        backgroundColor: theme.navy,
        borderRadius: 12,
        padding: '10px 16px',
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        boxShadow: '0 8px 24px rgba(0,0,0,0.3)',
        zIndex: 200,
        whiteSpace: 'nowrap',
      }}
    >
      <span style={{ fontSize: 13, fontWeight: 700, color: '#fff' }}>
        {count} selected
      </span>
      <div style={{ width: 1, height: 20, backgroundColor: 'rgba(255,255,255,0.2)' }} />
      <button onClick={onDismiss} style={{ padding: '5px 12px', borderRadius: 6, backgroundColor: 'rgba(255,255,255,0.12)', border: 'none', cursor: 'pointer', color: '#fff', fontSize: 13, fontWeight: 600 }}>
        Dismiss
      </button>
      <button onClick={onSnooze} style={{ padding: '5px 12px', borderRadius: 6, backgroundColor: 'rgba(255,255,255,0.12)', border: 'none', cursor: 'pointer', color: '#60a5fa', fontSize: 13, fontWeight: 600 }}>
        Snooze 7d
      </button>
      <button onClick={onExport} style={{ padding: '5px 12px', borderRadius: 6, backgroundColor: 'rgba(255,255,255,0.12)', border: 'none', cursor: 'pointer', color: '#34d399', fontSize: 13, fontWeight: 600 }}>
        Export
      </button>
      <button onClick={onClear} style={{ padding: '5px 8px', background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(255,255,255,0.5)', fontSize: 18, lineHeight: 1 }}>
        ×
      </button>
    </div>
  );
}

// ─── Bulk dismiss modal ───────────────────────────────────────────────────────

function BulkDismissModal({ visible, onClose, onConfirm, count, modalAction, theme }) {
  const [reason, setReason]  = useState('intentional');
  const [note, setNote]      = useState('');
  const [loading, setLoading] = useState(false);

  if (!visible) return null;

  async function handleConfirm() {
    if (reason === 'other' && !note.trim()) {
      alert('Please add a note when selecting "Other".');
      return;
    }
    setLoading(true);
    await onConfirm({ reason, note: note.trim() });
    setLoading(false);
  }

  return (
    <div
      style={{ position: 'fixed', inset: 0, backgroundColor: '#00000080', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}
      onClick={onClose}
    >
      <div
        onClick={e => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={`Bulk ${modalAction}`}
        style={{ backgroundColor: theme.surface, borderRadius: 16, padding: 24, maxWidth: 420, width: '90vw', boxShadow: '0 16px 40px rgba(0,0,0,0.3)' }}
      >
        <span style={{ fontSize: 17, fontWeight: 800, color: theme.text, display: 'block', marginBottom: 4 }}>
          {modalAction === 'dismiss' ? `Dismiss ${count} resources` : `Snooze ${count} resources`}
        </span>
        <span style={{ fontSize: 13, color: theme.textMuted, display: 'block', marginBottom: 16 }}>
          {modalAction === 'dismiss' ? 'These resources will be hidden from the ghost list.' : 'These resources will be hidden for 7 days.'}
        </span>

        <span style={{ fontSize: 11, fontWeight: 700, color: theme.textMuted, letterSpacing: 1, textTransform: 'uppercase', display: 'block', marginBottom: 8 }}>Reason</span>
        {DISMISS_REASONS.map(r => (
          <button
            key={r.value}
            onClick={() => setReason(r.value)}
            style={{
              display: 'flex', alignItems: 'center', gap: 10,
              padding: '9px 12px', borderRadius: 8, marginBottom: 4,
              border: `1px solid ${reason === r.value ? theme.accent : theme.border}`,
              backgroundColor: reason === r.value ? theme.accentLight : 'transparent',
              cursor: 'pointer', width: '100%', textAlign: 'left',
            }}
          >
            <div style={{ width: 15, height: 15, borderRadius: '50%', border: `2px solid ${reason === r.value ? theme.accent : theme.textMuted}`, backgroundColor: reason === r.value ? theme.accent : 'transparent', flexShrink: 0 }} />
            <span style={{ fontSize: 14, color: reason === r.value ? theme.accent : theme.textMid, fontWeight: reason === r.value ? 600 : 400 }}>{r.label}</span>
          </button>
        ))}

        {(reason === 'other' || note.length > 0) && (
          <textarea
            value={note}
            onChange={e => setNote(e.target.value)}
            placeholder={reason === 'other' ? 'Note (required)…' : 'Add a note (optional)…'}
            style={{ marginTop: 8, backgroundColor: theme.surfaceRaised, border: `1px solid ${theme.border}`, borderRadius: 8, padding: 12, color: theme.text, fontSize: 14, minHeight: 56, width: '100%', boxSizing: 'border-box', resize: 'vertical', outline: 'none' }}
          />
        )}

        <div style={{ display: 'flex', gap: 10, marginTop: 20 }}>
          <button onClick={onClose} style={{ flex: 1, padding: '12px', borderRadius: 10, border: `1px solid ${theme.border}`, backgroundColor: 'transparent', cursor: 'pointer' }}>
            <span style={{ color: theme.textMid, fontWeight: 700, fontSize: 14 }}>Cancel</span>
          </button>
          <button onClick={handleConfirm} disabled={loading} style={{ flex: 1, padding: '12px', borderRadius: 10, backgroundColor: theme.accent, border: 'none', cursor: 'pointer', opacity: loading ? 0.6 : 1, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            {loading ? <Spinner size={18} color="#fff" /> : <span style={{ color: '#fff', fontWeight: 800, fontSize: 14 }}>{modalAction === 'dismiss' ? 'Dismiss All' : 'Snooze All'}</span>}
          </button>
        </div>
      </div>
    </div>
  );
}

// ─── CSV export ───────────────────────────────────────────────────────────────

function exportCSV(list, toast) {
  const headers = ['resource_id', 'service', 'region', 'monthly_cost', 'currency', 'usage_metric', 'usage_avg', 'usage_unit', 'owner', 'is_ghost', 'reason'];
  const rows = list.map(r => [
    r.resource_id, r.service, r.region, r.monthly_cost.toFixed(2), r.currency,
    r.usage_metric ?? '', r.usage_avg ?? '', r.usage_unit ?? '',
    r.owner ?? '', r.is_ghost ? 'true' : 'false', r.reason ?? '',
  ].map(v => `"${String(v).replace(/"/g, '""')}"`).join(','));
  const csv = [headers.join(','), ...rows].join('\n');
  const blob = new Blob([csv], { type: 'text/csv' });
  const url  = URL.createObjectURL(blob);
  const a    = document.createElement('a');
  a.href     = url;
  a.download = `axiaops-ghosts-${new Date().toISOString().slice(0, 10)}.csv`;
  a.click();
  URL.revokeObjectURL(url);
  toast(`Exported ${list.length} resource${list.length !== 1 ? 's' : ''} to CSV`, 'success');
}

// ─── Sort function ────────────────────────────────────────────────────────────

function sortResources(list, sortBy) {
  return [...list].sort((a, b) => {
    switch (sortBy) {
      case 'cost_asc':  return a.monthly_cost - b.monthly_cost;
      case 'cost_desc': return b.monthly_cost - a.monthly_cost;
      case 'service':   return a.service.localeCompare(b.service);
      case 'region':    return a.region.localeCompare(b.region);
      default:          return b.monthly_cost - a.monthly_cost;
    }
  });
}

// ─── Main component ───────────────────────────────────────────────────────────

export default function DashboardScreen({
  onShowTrend, onSelectGhost, accounts = [], onConnectAccount, onEditAccount,
  selectedAccount, onSelectAccount,
}) {
  const { theme, isDark } = useTheme();
  const { toast }         = useToast();
  const queryClient       = useQueryClient();
  const t = theme;

  const [filterSvc, setFilterSvc]       = useState(null);
  const [filterOwner, setFilterOwner]   = useState(null);
  const [ghostOnly, setGhostOnly]       = useState(true);
  const [showDismissed, setShowDismissed] = useState(false);
  const [scanning, setScanning]         = useState(null);
  const [search, setSearch]             = useState('');
  const [sortBy, setSortBy]             = useState('cost_desc');
  const [selected, setSelected]         = useState(new Set());
  const [bulkModal, setBulkModal]       = useState(null); // 'dismiss' | 'snooze' | null

  const summary    = useQuery({ queryKey: ['summary', selectedAccount],    queryFn: () => fetchSummary(selectedAccount) });
  const resources  = useQuery({ queryKey: ['resources', selectedAccount],  queryFn: () => fetchResources(selectedAccount) });
  const trend      = useQuery({ queryKey: ['trend'],                       queryFn: () => fetchTrend(null) });
  const dismissals = useQuery({ queryKey: ['dismissals', selectedAccount], queryFn: () => fetchDismissals(selectedAccount) });

  const isLoading    = summary.isLoading || resources.isLoading;
  const isError      = summary.isError   || resources.isError;
  const isRefreshing = summary.isFetching || resources.isFetching;

  const dismissedSet = useMemo(() => {
    const set = new Set();
    (dismissals.data ?? []).forEach(d => set.add(d.resource_id));
    return set;
  }, [dismissals.data]);

  const owners = useMemo(() => {
    const set = new Set((resources.data ?? []).map(r => r.owner).filter(Boolean));
    return [...set].sort();
  }, [resources.data]);

  const byService = useMemo(
    () => Object.entries(summary.data?.by_service ?? {}).sort((a, b) => b[1].savings - a[1].savings),
    [summary.data],
  );

  function refresh() {
    summary.refetch(); resources.refetch(); trend.refetch(); dismissals.refetch();
  }

  function toggleSelect(id) {
    setSelected(prev => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }

  function toggleSelectAll(ids) {
    setSelected(prev => ids.every(id => prev.has(id)) ? new Set() : new Set(ids));
  }

  async function handleScan(accountId) {
    setScanning(accountId);
    try {
      await scanAccount(accountId);
      toast('Scan started — results will appear shortly', 'info');
      setTimeout(() => { refresh(); setScanning(null); }, 5000);
    } catch {
      toast('Scan failed. Please try again.', 'error');
      setScanning(null);
    }
  }

  async function handleBulkAction({ reason, note }) {
    const ids = [...selected];
    const items = (resources.data ?? []).filter(r => ids.includes(r.resource_id));
    const action = bulkModal === 'snooze' ? 'snooze' : 'dismiss';
    const snoozeUntil = action === 'snooze'
      ? new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString()
      : undefined;

    let succeeded = 0;
    for (const item of items) {
      try {
        await dismissGhost({
          accountId: item.internal_account_id, provider: item.provider,
          service: item.service, region: item.region, resourceId: item.resource_id,
          action, reason, note, snoozeUntil,
        });
        succeeded++;
      } catch {
        // skip already-dismissed
      }
    }

    queryClient.invalidateQueries({ queryKey: ['resources'] });
    queryClient.invalidateQueries({ queryKey: ['dismissals'] });
    setSelected(new Set());
    setBulkModal(null);
    toast(`${action === 'snooze' ? 'Snoozed' : 'Dismissed'} ${succeeded} resource${succeeded !== 1 ? 's' : ''}`, 'success');
  }

  const activeFilters = [
    filterSvc   && { key: 'svc',   label: serviceConfig(filterSvc).label },
    filterOwner && { key: 'owner', label: filterOwner },
  ].filter(Boolean);

  function clearFilter(key) {
    if (key === 'svc') setFilterSvc(null);
    if (key === 'owner') setFilterOwner(null);
  }

  // ── Loading state ──────────────────────────────────────────────────────────
  if (isLoading) {
    return (
      <div style={{ display: 'flex', flex: 1, justifyContent: 'center', alignItems: 'center', backgroundColor: t.bg, minHeight: '60vh', flexDirection: 'column', gap: 14 }}>
        <Spinner size={32} color={t.accent} />
        <span style={{ color: t.textMuted, fontSize: 14 }}>Analysing resources…</span>
      </div>
    );
  }

  if (isError) {
    return (
      <div style={{ display: 'flex', flex: 1, justifyContent: 'center', alignItems: 'center', backgroundColor: t.bg, minHeight: '60vh', flexDirection: 'column', gap: 12, padding: 32 }}>
        <span style={{ fontSize: 28, color: t.textMuted }}>⚠</span>
        <span style={{ fontSize: 17, fontWeight: 700, color: t.text }}>Service unavailable</span>
        <span style={{ fontSize: 13, color: t.textMuted, textAlign: 'center', lineHeight: '20px' }}>
          Make sure the API service is running
        </span>
        <button
          onClick={refresh}
          style={{ marginTop: 12, backgroundColor: t.accent, padding: '10px 24px', borderRadius: 8, border: 'none', cursor: 'pointer' }}
        >
          <span style={{ color: '#fff', fontWeight: 700, fontSize: 14 }}>Retry</span>
        </button>
      </div>
    );
  }

  // ── Compute list ───────────────────────────────────────────────────────────
  const listData = (() => {
    if (showDismissed) return dismissals.data ?? [];
    let list = resources.data ?? [];
    if (ghostOnly) list = list.filter(r => r.is_ghost);
    list = list.filter(r => !dismissedSet.has(r.resource_id));
    if (filterSvc)   list = list.filter(r => r.service === filterSvc);
    if (filterOwner) list = list.filter(r => r.owner === filterOwner);
    if (search.trim()) {
      const q = search.toLowerCase();
      list = list.filter(r =>
        r.resource_id.toLowerCase().includes(q) ||
        (r.owner ?? '').toLowerCase().includes(q) ||
        r.service.toLowerCase().includes(q) ||
        r.region.toLowerCase().includes(q)
      );
    }
    return sortResources(list, sortBy);
  })();

  const visibleIds = listData.filter(r => r.resource_id).map(r => r.resource_id);
  const allSelected = visibleIds.length > 0 && visibleIds.every(id => selected.has(id));
  const selectedItems = (resources.data ?? []).filter(r => selected.has(r.resource_id));

  // ── Render ─────────────────────────────────────────────────────────────────
  return (
    <div style={{ backgroundColor: t.bg, minHeight: '100%', paddingBottom: selected.size > 0 ? 100 : 40 }}>

      {/* Account selector + refresh bar */}
      <div style={{
        backgroundColor: t.surface,
        borderBottom: `1px solid ${t.border}`,
        padding: '10px 16px',
        display: 'flex',
        alignItems: 'center',
        gap: 10,
      }}>
        {accounts.length > 0 ? (
          <AccountSelector
            accounts={accounts}
            selectedAccount={selectedAccount}
            onSelectAccount={onSelectAccount}
            onConnectAccount={onConnectAccount}
            onEditAccount={onEditAccount}
            onScanAccount={handleScan}
            scanning={scanning}
          />
        ) : (
          <button
            onClick={onConnectAccount}
            style={{ border: `1px dashed ${t.accent}`, borderRadius: 8, padding: '6px 14px', background: 'none', cursor: 'pointer' }}
          >
            <span style={{ color: t.accent, fontSize: 13, fontWeight: 600 }}>+ Connect AWS Account</span>
          </button>
        )}
        <div style={{ flex: 1 }} />
        {isRefreshing && <Spinner size={14} color={t.accent} />}
        <button
          onClick={refresh}
          aria-label="Refresh data"
          style={{ padding: '5px 8px', background: 'none', border: 'none', cursor: 'pointer' }}
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke={t.textMuted} strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="23 4 23 10 17 10" /><polyline points="1 20 1 14 7 14" />
            <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
          </svg>
        </button>
      </div>

      {/* Summary cards */}
      <SummaryCards summary={summary} trend={trend} onShowTrend={onShowTrend} theme={t} />

      {/* Filter pills */}
      <div style={{ padding: '12px 16px 0' }}>
        <FilterPills
          byService={byService}
          owners={owners}
          filterSvc={filterSvc}
          filterOwner={filterOwner}
          onFilterSvc={setFilterSvc}
          onFilterOwner={setFilterOwner}
          currency={summary.data?.currency ?? '$'}
          theme={t}
          isDark={isDark}
        />
      </div>

      {/* Tab row */}
      <div style={{ display: 'flex', gap: 6, padding: '0 16px 0', marginTop: 4 }}>
        {[
          { label: 'Zombies', active: ghostOnly && !showDismissed, onClick: () => { setGhostOnly(true); setShowDismissed(false); } },
          { label: 'All', active: !ghostOnly && !showDismissed, onClick: () => { setGhostOnly(false); setShowDismissed(false); } },
          (dismissals.data?.length ?? 0) > 0 && {
            label: `Dismissed (${dismissals.data?.length})`,
            active: showDismissed,
            onClick: () => setShowDismissed(v => !v),
          },
        ].filter(Boolean).map(({ label, active, onClick }) => (
          <button
            key={label}
            onClick={onClick}
            aria-pressed={active}
            style={{
              padding: '7px 14px',
              borderRadius: 20,
              backgroundColor: active ? t.navy : t.surfaceRaised,
              border: `1px solid ${active ? t.navy : t.border}`,
              cursor: 'pointer',
              fontSize: 13,
              fontWeight: 600,
              color: active ? '#fff' : t.textMid,
            }}
          >
            {label}
          </button>
        ))}
      </div>

      {/* Search + sort (only for non-dismissed view) */}
      {!showDismissed && (
        <FilterBar
          search={search}
          onSearch={setSearch}
          sortBy={sortBy}
          onSort={setSortBy}
          theme={t}
          activeFilters={activeFilters}
          onClearFilter={clearFilter}
        />
      )}

      {/* Section header */}
      <div style={{ display: 'flex', alignItems: 'center', padding: '4px 16px 8px' }}>
        {!showDismissed && (
          <input
            type="checkbox"
            checked={allSelected}
            onChange={() => toggleSelectAll(visibleIds)}
            aria-label="Select all resources"
            style={{ width: 15, height: 15, accentColor: t.accent, marginRight: 10, cursor: 'pointer' }}
          />
        )}
        <span style={{ flex: 1, fontSize: 11, fontWeight: 700, color: t.textMuted, letterSpacing: 1.2, textTransform: 'uppercase' }}>
          {showDismissed ? 'Dismissed Resources' : ghostOnly ? `Ghost Resources` : 'All Resources'}
          {!showDismissed && ` · ${listData.length}`}
        </span>
        {!showDismissed && (
          <button
            onClick={() => exportCSV(listData, toast)}
            aria-label="Export to CSV"
            style={{ padding: '4px 10px', borderRadius: 6, border: `1px solid ${t.border}`, backgroundColor: t.surfaceRaised, cursor: 'pointer' }}
          >
            <span style={{ fontSize: 11, fontWeight: 700, color: t.textMid }}>↓ CSV</span>
          </button>
        )}
      </div>

      {/* Empty state */}
      {listData.length === 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', padding: '48px 32px', gap: 8 }}>
          <span style={{ fontSize: 32 }}>🎉</span>
          <span style={{ fontSize: 16, fontWeight: 700, color: t.text }}>
            {ghostOnly && !showDismissed ? 'No zombie resources found!' : 'No resources match your filters'}
          </span>
          <span style={{ fontSize: 13, color: t.textMuted, textAlign: 'center' }}>
            {ghostOnly && !showDismissed
              ? 'All your AWS resources appear to be actively used.'
              : 'Try adjusting the search or removing filters.'}
          </span>
        </div>
      )}

      {/* Resource list */}
      {listData.map((item) => (
        showDismissed
          ? <DismissedCard key={String(item.id)} item={item} theme={t} isDark={isDark} />
          : <ResourceCard
              key={item.resource_id}
              item={item}
              onSelect={onSelectGhost}
              isSelected={selected.has(item.resource_id)}
              onToggleSelect={toggleSelect}
              theme={t}
              isDark={isDark}
            />
      ))}

      {/* Bulk action bar */}
      {selected.size > 0 && (
        <BulkActionBar
          count={selected.size}
          onDismiss={() => setBulkModal('dismiss')}
          onSnooze={() => setBulkModal('snooze')}
          onExport={() => exportCSV(selectedItems, toast)}
          onClear={() => setSelected(new Set())}
          theme={t}
        />
      )}

      {/* Bulk dismiss/snooze modal */}
      <BulkDismissModal
        visible={!!bulkModal}
        onClose={() => setBulkModal(null)}
        onConfirm={handleBulkAction}
        count={selected.size}
        modalAction={bulkModal}
        theme={t}
      />
    </div>
  );
}
