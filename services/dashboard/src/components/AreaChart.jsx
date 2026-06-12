import { useState, useRef, useId, useEffect, useMemo } from 'react';

const CHART_HEIGHT = 200;
// Default desktop margins. The 56px left gutter holds 5-character y-axis
// labels like "$1.2k" with breathing room — fine on a 1280px chart, but
// at 360px viewport width the same 56px eats 17% of the plot. The
// component picks a narrower left margin (40px) below 480px CSS pixels;
// "$10k" still fits at 40px, and the smaller gutter buys ~16px of plot.
const MARGIN = { top: 16, right: 20, bottom: 32, left: 56 };
const MARGIN_LEFT_NARROW = 40;

function formatCost(val) {
  if (val >= 10000) return `${(val / 1000).toFixed(0)}k`;
  if (val >= 1000) return `${(val / 1000).toFixed(1)}k`;
  return val.toFixed(0);
}

function formatDate(iso) {
  return new Date(iso).toLocaleDateString('en-GB', { day: 'numeric', month: 'short' });
}

// Year-bearing variant for the tooltip, where the exact date matters and
// there's room. The x-axis stays compact (no year) to avoid clutter; the
// section caption carries the overall range + year.
function formatDateFull(iso) {
  return new Date(iso).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
}

// Month label for the x-axis — month name only, no year. The year lives in
// the section caption ("3 Jun 2025 – 2 Jun 2026") and the hover tooltip, so
// putting it on the axis would be redundant and the compact form ("Jan 26")
// reads as a day-of-month rather than a year.
function formatMonth(iso) {
  return new Date(iso).toLocaleDateString('en-GB', { month: 'short' });
}

/**
 * AreaChart — Interactive SVG area chart for cost/zombie trends
 *
 * @param {Array} data - Array of {total_monthly_cost, snapshot_at, zombie_count, currency}
 * @param {String} selectedId - snapshot_at timestamp of selected point
 * @param {Function} onSelect - Called with data point when clicked
 * @param {Object} theme - Theme object
 * @param {Number} screenWidth - First-paint width fallback (px). The chart is
 *        container-responsive at runtime via ResizeObserver; this prop only
 *        seeds the width for the initial render before the observer fires.
 */
export default function AreaChart({ data, selectedId, onSelect, screenWidth }) {
  const [hoverIdx, setHoverIdx] = useState(null);
  const svgRef = useRef(null);
  const wrapRef = useRef(null);
  const reactId = useId();
  const gradientId = `area-chart-grad-${reactId}`;

  // Container-responsive sizing. Earlier the SVG width was derived from a
  // window-width prop, so the chart couldn't fill a width-capped column —
  // it overflowed or under-filled whenever its container wasn't the full
  // window. Now we measure the wrapper itself (clientWidth minus its 16px
  // horizontal padding) and reflow on any container resize, not just window
  // resize. `screenWidth` survives only as the pre-measurement fallback.
  const [measuredWidth, setMeasuredWidth] = useState(Math.max(320, (screenWidth ?? 800) - 32));
  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const measure = () => setMeasuredWidth(Math.max(320, el.clientWidth - 32));
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // All chart geometry derives from (data, measuredWidth) only — never from
  // hoverIdx. Memoize the whole block so a mousemove (which only flips
  // hoverIdx) doesn't re-run the scales, path string-joins, and per-point
  // date parsing. On a 1-year view (365 points) this is the difference
  // between smooth hovering and frame-rate jank. Returns null for the
  // too-little-data case so the early-return stays a single seam.
  const geometry = useMemo(() => {
    if (!data || data.length < 2) return null;

    const width = measuredWidth;
    const marginLeft = width < 480 ? MARGIN_LEFT_NARROW : MARGIN.left;
    const plotW = width - marginLeft - MARGIN.right;
    const plotH = CHART_HEIGHT - MARGIN.top - MARGIN.bottom;

    const values = data.map(s => s.total_monthly_cost);
    const rawMax = Math.max(...values);
    const rawMin = Math.min(...values);
    const range = rawMax - rawMin || rawMax * 0.1 || 1;
    const maxVal = rawMax + range * 0.08;
    const minVal = Math.max(0, rawMin - range * 0.05);
    const valRange = maxVal - minVal || 1;

    // Map data to pixel coordinates
    const points = data.map((s, i) => ({
      x: marginLeft + (i / (data.length - 1)) * plotW,
      y: MARGIN.top + plotH - ((s.total_monthly_cost - minVal) / valRange) * plotH,
    }));

    // SVG paths
    const linePath = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${p.x},${p.y}`).join(' ');
    const areaPath = linePath
      + ` L${points[points.length - 1].x},${MARGIN.top + plotH}`
      + ` L${points[0].x},${MARGIN.top + plotH} Z`;

    // Y-axis: 4 ticks
    const yTicks = [0, 1, 2, 3].map(i => {
      const val = minVal + valRange * (i / 3);
      const y = MARGIN.top + plotH - (i / 3) * plotH;
      return { val, y };
    });

    // X-axis labels — first-of-month ticks so the cadence reads as calendar
    // months. Width-aware: show every month when the plot is wide enough, then
    // thin to every Nth month as space tightens so labels never collide (a full
    // year shows all ~12 months on desktop, ~4 on a phone). Falls back to
    // evenly-spaced date ticks when the range is shorter than two months.
    const MIN_LABEL_PX = 64;
    const monthBoundaries = [];
    let prevMonthKey = null;
    data.forEach((s, i) => {
      const d = new Date(s.snapshot_at);
      const key = `${d.getFullYear()}-${d.getMonth()}`;
      if (key !== prevMonthKey) { monthBoundaries.push(i); prevMonthKey = key; }
    });

    let xLabels;
    if (monthBoundaries.length >= 2) {
      const maxLabels = Math.max(2, Math.floor(plotW / MIN_LABEL_PX));
      const stride = Math.ceil(monthBoundaries.length / maxLabels);
      xLabels = monthBoundaries
        .filter((_, i) => i % stride === 0)
        .map((idx) => ({ x: points[idx].x, label: formatMonth(data[idx].snapshot_at) }));
    } else {
      const xLabelCount = Math.min(6, data.length);
      xLabels = [];
      for (let i = 0; i < xLabelCount; i++) {
        const idx = Math.round(i * (data.length - 1) / (xLabelCount - 1));
        xLabels.push({ x: points[idx].x, label: formatDate(data[idx].snapshot_at) });
      }
    }

    return { width, marginLeft, plotH, points, linePath, areaPath, yTicks, xLabels };
  }, [data, measuredWidth]);

  if (!geometry) return null;

  const { width, marginLeft, plotH, points, linePath, areaPath, yTicks, xLabels } = geometry;

  // Find nearest data point to cursor
  function handleMouseMove(e) {
    const rect = svgRef.current?.getBoundingClientRect();
    if (!rect) return;
    const mouseX = e.clientX - rect.left;
    let closest = 0;
    let minDist = Infinity;
    for (let i = 0; i < points.length; i++) {
      const dist = Math.abs(points[i].x - mouseX);
      if (dist < minDist) { minDist = dist; closest = i; }
    }
    setHoverIdx(closest);
  }

  function handleClick() {
    // onSelect is optional — read-only consumers (e.g. the org-summary trend)
    // render the chart without a per-point drill-down.
    if (hoverIdx !== null && onSelect) onSelect(data[hoverIdx]);
  }

  const hoverPoint = hoverIdx !== null ? points[hoverIdx] : null;
  const hoverSnap = hoverIdx !== null ? data[hoverIdx] : null;
  const selectedPointIdx = selectedId ? data.findIndex(s => s.snapshot_at === selectedId) : -1;
  const selectedPoint = selectedPointIdx >= 0 ? points[selectedPointIdx] : null;

  // Tooltip position — keep it within the chart bounds
  const tooltipW = 140;
  const tooltipLeft = hoverPoint
    ? Math.max(0, Math.min(hoverPoint.x - tooltipW / 2, width - tooltipW))
    : 0;

  return (
    <div ref={wrapRef} style={{ padding: '0 16px', position: 'relative' }}>
      <svg
        ref={svgRef}
        width={width}
        height={CHART_HEIGHT}
        style={{ display: 'block', cursor: 'crosshair' }}
        onMouseMove={handleMouseMove}
        onMouseLeave={() => setHoverIdx(null)}
        onClick={handleClick}
      >
        <defs>
          <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={'var(--color-accent)'} stopOpacity="0.25" />
            <stop offset="100%" stopColor={'var(--color-accent)'} stopOpacity="0.02" />
          </linearGradient>
        </defs>

        {/* Horizontal grid lines */}
        {yTicks.map((tick, i) => (
          <line key={i} x1={marginLeft} y1={tick.y} x2={width - MARGIN.right} y2={tick.y}
            stroke={'var(--color-border)'} strokeWidth="1" opacity="0.4" />
        ))}

        {/* Area fill */}
        <path d={areaPath} fill={`url(#${gradientId})`} />

        {/* Line */}
        <path d={linePath} fill="none" stroke={'var(--color-accent)'} strokeWidth="2"
          strokeLinecap="round" strokeLinejoin="round" />

        {/* Y-axis labels */}
        {yTicks.map((tick, i) => (
          <text key={i} x={marginLeft - 10} y={tick.y + 4} textAnchor="end"
            fontSize="10" fontFamily="system-ui, sans-serif" fill={'var(--color-text-muted)'}>
            ${formatCost(tick.val)}
          </text>
        ))}

        {/* X-axis labels */}
        {xLabels.map((l, i) => (
          <text key={i} x={l.x} y={CHART_HEIGHT - 8} textAnchor="middle"
            fontSize="10" fontFamily="system-ui, sans-serif" fill={'var(--color-text-muted)'}>
            {l.label}
          </text>
        ))}

        {/* Selected point */}
        {selectedPoint && (
          <>
            <line x1={selectedPoint.x} y1={MARGIN.top} x2={selectedPoint.x} y2={MARGIN.top + plotH}
              stroke={'var(--color-accent)'} strokeWidth="1" strokeDasharray="4 3" opacity="0.6" />
            <circle cx={selectedPoint.x} cy={selectedPoint.y} r="5"
              fill={'var(--color-accent)'} stroke={'var(--color-surface)'} strokeWidth="2" />
          </>
        )}

        {/* Hover crosshair + dot */}
        {hoverPoint && hoverIdx !== selectedPointIdx && (
          <>
            <line x1={hoverPoint.x} y1={MARGIN.top} x2={hoverPoint.x} y2={MARGIN.top + plotH}
              stroke={'var(--color-text-muted)'} strokeWidth="1" opacity="0.3" />
            <circle cx={hoverPoint.x} cy={hoverPoint.y} r="4"
              fill={'var(--color-accent)'} stroke={'var(--color-surface)'} strokeWidth="2" />
          </>
        )}
      </svg>

      {/* Hover tooltip */}
      {hoverSnap && (
        <div style={{
          position: 'absolute',
          top: 4,
          left: tooltipLeft,
          width: tooltipW,
          backgroundColor: 'var(--color-surface)',
          border: `1px solid var(--color-border)`,
          borderRadius: 8,
          padding: '8px 10px',
          pointerEvents: 'none',
          boxShadow: '0 4px 12px rgba(0,0,0,0.15)',
          zIndex: 10,
        }}>
          <span style={{ fontSize: 11, color: 'var(--color-text-muted)', display: 'block', marginBottom: 2 }}>
            {formatDateFull(hoverSnap.snapshot_at)}
          </span>
          <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--color-accent)', display: 'block' }}>
            ${hoverSnap.total_monthly_cost.toFixed(2)}
          </span>
          {hoverSnap.zombie_count !== undefined && (
            <span style={{ fontSize: 11, color: 'var(--color-text-muted)', display: 'block', marginTop: 1 }}>
              {hoverSnap.zombie_count} zombie{hoverSnap.zombie_count !== 1 ? 's' : ''}
            </span>
          )}
        </div>
      )}
    </div>
  );
}
