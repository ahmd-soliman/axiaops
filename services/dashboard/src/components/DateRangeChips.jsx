import { useState, useRef, useEffect } from 'react';
import { formatDateRange } from '../utils/formatDate';

// Preset windows shared across every chart screen (TrendScreen, CostAnalytics,
// Overview). Sourced from the local PERIOD_OPTIONS constants that each screen
// used to declare independently before this component existed.
export const PRESET_OPTIONS = [
  { label: '7d',  days: 7 },
  { label: '30d', days: 30 },
  { label: '90d', days: 90 },
  { label: '6m',  days: 180 },
  { label: '1y',  days: 365 },
];

// Default selection when a screen mounts without a remembered value.
export const DEFAULT_DAYS = 30;

function isoToday() { return new Date().toISOString().slice(0, 10); }
function isoDaysAgo(n) {
  const d = new Date(); d.setDate(d.getDate() - n);
  return d.toISOString().slice(0, 10);
}

function daysBetween(startIso, endIso) {
  const start = new Date(startIso + 'T00:00:00Z');
  const end = new Date(endIso + 'T00:00:00Z');
  return Math.max(1, Math.round((end - start) / (1000 * 60 * 60 * 24)));
}

// Module-level chip style — only depends on its two boolean args, so it
// needn't be redefined as a closure on every render.
function chipStyle(mobile, active) {
  return {
    padding: mobile ? '7px 12px' : '4px 10px',
    borderRadius: 6,
    border: `1px solid ${active ? 'var(--color-accent)' : 'var(--color-border)'}`,
    backgroundColor: active ? 'var(--color-accent)' : 'var(--color-surface-raised)',
    color: active ? 'var(--color-text-on-dark)' : 'var(--color-text-mid)',
    fontWeight: 700,
    fontSize: 12,
    cursor: 'pointer',
    whiteSpace: 'nowrap',
    flexShrink: 0,
  };
}

// DateRangeChips
//
// Preset chips (7d / 30d / 90d / 6m / 1y) + a Custom… trigger that opens a
// small popover with two date inputs (start, end). Lifts the picker UI out of
// TrendScreen + CostAnalyticsScreen into one component so the same UX renders
// on every chart screen (including Overview, which had no picker before).
//
// Contract:
//   - `value` is the currently-selected window in days.
//   - When the user picks a preset, onChange is called with that day count.
//   - When the user enters a custom range, onChange is called with the day
//     count between the chosen dates AND a {sinceIso, untilIso} pair so the
//     screen can scope to that window precisely (callers that only care
//     about a day count can ignore the second arg).
//
// Visual parity with the previous in-screen chip rows is preserved — same
// border / accent / padding so refactoring TrendScreen + CostAnalyticsScreen
// is a drop-in swap.
export default function DateRangeChips({ value, onChange, mobile = false, presets = PRESET_OPTIONS }) {
  const [open, setOpen] = useState(false);
  const [customSince, setCustomSince] = useState(isoDaysAgo(value > 0 ? value : DEFAULT_DAYS));
  const [customUntil, setCustomUntil] = useState(isoToday());
  const popoverRef = useRef(null);

  const isCustom = !presets.some(p => p.days === value);

  useEffect(() => {
    if (!open) return;
    function onDocClick(e) {
      if (popoverRef.current && !popoverRef.current.contains(e.target)) setOpen(false);
    }
    // pointerdown covers mouse + touch + pen in one event, sidestepping the
    // ~300ms-delayed synthetic mousedown iOS Safari emits after taps (which
    // would close the popover after the user has already started interacting
    // with the date input inside it).
    document.addEventListener('pointerdown', onDocClick);
    return () => document.removeEventListener('pointerdown', onDocClick);
  }, [open]);

  function applyCustom() {
    if (!customSince || !customUntil) return;
    if (customSince > customUntil) return;
    const days = daysBetween(customSince, customUntil);
    onChange(days, { sinceIso: customSince, untilIso: customUntil });
    setOpen(false);
  }

  return (
    <div role="group" aria-label="Select time period" style={{ display: 'flex', gap: 4, position: 'relative' }}>
      {presets.map(p => {
        const active = value === p.days;
        return (
          <button
            key={p.days}
            type="button"
            onClick={() => onChange(p.days)}
            aria-pressed={active}
            style={chipStyle(mobile, active)}
          >
            {p.label}
          </button>
        );
      })}
      <button
        type="button"
        onClick={() => setOpen(o => !o)}
        aria-pressed={isCustom}
        aria-haspopup="dialog"
        aria-expanded={open}
        style={chipStyle(mobile, isCustom)}
      >
        {/* Once a Custom range is applied, label the chip with the selected
            window in the app's locale-unambiguous "1–15 May 2026" form, so the
            persistent display never depends on the native input's MM/DD vs DD/MM
            locale rendering. Falls back to "Custom…" before any selection. */}
        {isCustom ? formatDateRange(customSince, customUntil) : 'Custom…'}
      </button>

      {open && (
        <div
          ref={popoverRef}
          role="dialog"
          aria-label="Custom date range"
          style={{
            position: 'absolute',
            top: '100%', right: 0,
            marginTop: 6,
            padding: 12,
            background: 'var(--color-surface-raised)',
            border: '1px solid var(--color-border)',
            borderRadius: 8,
            boxShadow: '0 6px 24px rgba(0,0,0,0.18)',
            // 150: same tier as AvatarMenu / OrgSwitcher (anchored panel opened
            // by a button click) — sits above InfoTooltip (z=50) so a tooltip
            // rendered behind a nearby chart can't float over the open popover,
            // and below AppShell (z=100) so global banners still occlude
            // correctly.
            zIndex: 150,
            display: 'flex',
            flexDirection: 'column',
            gap: 8,
            minWidth: 220,
          }}
        >
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 11, color: 'var(--color-text-muted)' }}>
            From
            <input
              type="date"
              value={customSince}
              max={customUntil || isoToday()}
              onChange={e => setCustomSince(e.target.value)}
              style={{ padding: '6px 8px', border: '1px solid var(--color-border)', borderRadius: 4, fontSize: 13 }}
            />
          </label>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 11, color: 'var(--color-text-muted)' }}>
            To
            <input
              type="date"
              value={customUntil}
              min={customSince}
              max={isoToday()}
              onChange={e => setCustomUntil(e.target.value)}
              style={{ padding: '6px 8px', border: '1px solid var(--color-border)', borderRadius: 4, fontSize: 13 }}
            />
          </label>
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 6, marginTop: 2 }}>
            <button
              type="button"
              onClick={() => setOpen(false)}
              style={{
                padding: '6px 10px', borderRadius: 6,
                border: '1px solid var(--color-border)', background: 'transparent',
                color: 'var(--color-text-mid)', fontSize: 12, cursor: 'pointer',
              }}
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={applyCustom}
              disabled={!customSince || !customUntil || customSince > customUntil}
              style={{
                padding: '6px 10px', borderRadius: 6,
                border: '1px solid var(--color-accent)', background: 'var(--color-accent)',
                color: 'var(--color-text-on-dark)', fontSize: 12, fontWeight: 700,
                cursor: 'pointer',
              }}
            >
              Apply
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
