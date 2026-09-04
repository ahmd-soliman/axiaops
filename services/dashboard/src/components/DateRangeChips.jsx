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
//   - `activeRange` is the currently-APPLIED custom {sinceIso, untilIso},
//     mirrored back in from the caller's own state (null while a preset is
//     active). Without this the component has no way to know what window is
//     actually in effect — it only ever sees a day count — so its "Custom…"
//     chip label falls back to guessing "today minus N days," which reads
//     wrong for any historical range (e.g. picking 1–30 Jun while viewing the
//     dashboard in September). Passing the real applied dates back in keeps
//     the label truthful across re-renders, remounts, and restored state.
//
// Visual parity with the previous in-screen chip rows is preserved — same
// border / accent / padding so refactoring TrendScreen + CostAnalyticsScreen
// is a drop-in swap.
export default function DateRangeChips({ value, onChange, mobile = false, presets = PRESET_OPTIONS, activeRange = null }) {
  const [open, setOpen] = useState(false);
  const [customSince, setCustomSince] = useState(activeRange?.sinceIso ?? isoDaysAgo(value > 0 ? value : DEFAULT_DAYS));
  const [customUntil, setCustomUntil] = useState(activeRange?.untilIso ?? isoToday());
  const popoverRef = useRef(null);

  const isCustom = activeRange != null || !presets.some(p => p.days === value);

  // Keep the popover draft (and the chip label, which reads customSince/
  // customUntil) in sync with the actually-applied range whenever the
  // caller's state changes out from under us — e.g. a fresh mount that
  // restores an existing custom selection, rather than only the moment the
  // user clicks Apply in this instance of the popover.
  useEffect(() => {
    if (!activeRange) return;
    setCustomSince(activeRange.sinceIso);
    setCustomUntil(activeRange.untilIso);
    // Deliberately depend on the primitive fields, not `activeRange` itself —
    // callers pass a fresh object literal on every render, and keying off
    // the reference would re-run (and needlessly reset any in-progress
    // popover edit) on every unrelated parent re-render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeRange?.sinceIso, activeRange?.untilIso]);

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

  // Opening the popover re-seeds the draft from whatever is CURRENTLY active,
  // rather than leaving it at whatever it last happened to be. Without this,
  // switching between preset chips (which never touch customSince/
  // customUntil at all — only Apply or the activeRange-sync effect above do)
  // left the popover showing a stale draft: pick 1y, open Custom (draft
  // reflects 1y — fine so far), Cancel, click 6m, reopen Custom — the draft
  // still read "1y" because nothing had ever recomputed it for the 6m
  // selection. Presets get a fresh trailing-window draft; an already-applied
  // Custom range gets its real dates back.
  function openPopover() {
    if (activeRange) {
      setCustomSince(activeRange.sinceIso);
      setCustomUntil(activeRange.untilIso);
    } else {
      setCustomSince(isoDaysAgo(value > 0 ? value : DEFAULT_DAYS));
      setCustomUntil(isoToday());
    }
    setOpen(true);
  }

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
        onClick={() => (open ? setOpen(false) : openPopover())}
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
              // Force dd/mm/yyyy display regardless of OS locale, matching
              // the en-GB convention formatDate.js uses everywhere else in
              // the app — the input's native picker otherwise renders
              // mm/dd/yyyy on a US-locale OS, which reads as day/month
              // transposed next to every other date in this app.
              lang="en-GB"
              value={customSince}
              // 395 = 365 + a 30-day buffer, not an arbitrary round number:
              // a plain 365-day floor only guarantees the same calendar DAY
              // a year ago is still retrievable -- it doesn't guarantee the
              // whole MONTH a year ago is, since a month can start up to
              // ~30 days before its end. The extra 30 days keeps a full
              // year-over-year monthly comparison valid no matter where in
              // the current month you are. Keep in sync with
              // COST_RECORDS_RETENTION_DAYS -- this is a UI-side ceiling,
              // not a substitute for retention actually covering the range.
              min={isoDaysAgo(395)}
              max={customUntil || isoToday()}
              onChange={e => setCustomSince(e.target.value)}
              style={{ padding: '6px 8px', border: '1px solid var(--color-border)', borderRadius: 4, fontSize: 13 }}
            />
          </label>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 11, color: 'var(--color-text-muted)' }}>
            To
            <input
              type="date"
              lang="en-GB"
              value={customUntil}
              min={customSince || isoDaysAgo(395)}
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
