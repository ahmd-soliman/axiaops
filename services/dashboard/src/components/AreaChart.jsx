import { useState, useRef, useCallback } from 'react';

const CHART_HEIGHT = 200;
const MARGIN = { top: 16, right: 20, bottom: 32, left: 56 };

function formatCost(val) {
  if (val >= 10000) return `${(val / 1000).toFixed(0)}k`;
  if (val >= 1000) return `${(val / 1000).toFixed(1)}k`;
  return val.toFixed(0);
}

function formatDate(iso) {
  return new Date(iso).toLocaleDateString('en-GB', { day: 'numeric', month: 'short' });
}

/**
 * AreaChart — Interactive SVG area chart for cost/ghost trends
 *
 * @param {Array} data - Array of {total_monthly_cost, snapshot_at, ghost_count, currency}
 * @param {String} selectedId - snapshot_at timestamp of selected point
 * @param {Function} onSelect - Called with data point when clicked
 * @param {Object} theme - Theme object
 * @param {Number} screenWidth - Screen width for responsive sizing
 */
export default function AreaChart({ data, selectedId, onSelect, theme, screenWidth }) {
  const [hoverIdx, setHoverIdx] = useState(null);
  const svgRef = useRef(null);
  const gradientId = 'area-chart-grad';

  if (!data || data.length < 2) return null;

  const t = theme;
  const width = Math.max(320, screenWidth - 32);
  const plotW = width - MARGIN.left - MARGIN.right;
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
    x: MARGIN.left + (i / (data.length - 1)) * plotW,
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

  // X-axis: ~5 evenly spaced labels
  const xLabelCount = Math.min(6, data.length);
  const xLabels = [];
  for (let i = 0; i < xLabelCount; i++) {
    const idx = Math.round(i * (data.length - 1) / (xLabelCount - 1));
    xLabels.push({ x: points[idx].x, label: formatDate(data[idx].snapshot_at) });
  }

  // Find nearest data point to cursor
  const handleMouseMove = useCallback((e) => {
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
  }, [data.length, width]);

  function handleClick() {
    if (hoverIdx !== null) onSelect(data[hoverIdx]);
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
    <div style={{ padding: '0 16px', position: 'relative' }}>
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
            <stop offset="0%" stopColor={t.accent} stopOpacity="0.25" />
            <stop offset="100%" stopColor={t.accent} stopOpacity="0.02" />
          </linearGradient>
        </defs>

        {/* Horizontal grid lines */}
        {yTicks.map((tick, i) => (
          <line key={i} x1={MARGIN.left} y1={tick.y} x2={width - MARGIN.right} y2={tick.y}
            stroke={t.border} strokeWidth="1" opacity="0.4" />
        ))}

        {/* Area fill */}
        <path d={areaPath} fill={`url(#${gradientId})`} />

        {/* Line */}
        <path d={linePath} fill="none" stroke={t.accent} strokeWidth="2"
          strokeLinecap="round" strokeLinejoin="round" />

        {/* Y-axis labels */}
        {yTicks.map((tick, i) => (
          <text key={i} x={MARGIN.left - 10} y={tick.y + 4} textAnchor="end"
            fontSize="10" fontFamily="system-ui, sans-serif" fill={t.textMuted}>
            ${formatCost(tick.val)}
          </text>
        ))}

        {/* X-axis labels */}
        {xLabels.map((l, i) => (
          <text key={i} x={l.x} y={CHART_HEIGHT - 8} textAnchor="middle"
            fontSize="10" fontFamily="system-ui, sans-serif" fill={t.textMuted}>
            {l.label}
          </text>
        ))}

        {/* Selected point */}
        {selectedPoint && (
          <>
            <line x1={selectedPoint.x} y1={MARGIN.top} x2={selectedPoint.x} y2={MARGIN.top + plotH}
              stroke={t.accent} strokeWidth="1" strokeDasharray="4 3" opacity="0.6" />
            <circle cx={selectedPoint.x} cy={selectedPoint.y} r="5"
              fill={t.accent} stroke={t.surface} strokeWidth="2" />
          </>
        )}

        {/* Hover crosshair + dot */}
        {hoverPoint && hoverIdx !== selectedPointIdx && (
          <>
            <line x1={hoverPoint.x} y1={MARGIN.top} x2={hoverPoint.x} y2={MARGIN.top + plotH}
              stroke={t.textMuted} strokeWidth="1" opacity="0.3" />
            <circle cx={hoverPoint.x} cy={hoverPoint.y} r="4"
              fill={t.accent} stroke={t.surface} strokeWidth="2" />
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
          backgroundColor: t.surface,
          border: `1px solid ${t.border}`,
          borderRadius: 8,
          padding: '8px 10px',
          pointerEvents: 'none',
          boxShadow: '0 4px 12px rgba(0,0,0,0.15)',
          zIndex: 10,
        }}>
          <span style={{ fontSize: 11, color: t.textMuted, display: 'block', marginBottom: 2 }}>
            {formatDate(hoverSnap.snapshot_at)}
          </span>
          <span style={{ fontSize: 15, fontWeight: 800, color: t.accent, display: 'block' }}>
            ${hoverSnap.total_monthly_cost.toFixed(2)}
          </span>
          {hoverSnap.ghost_count !== undefined && (
            <span style={{ fontSize: 11, color: t.textMuted, display: 'block', marginTop: 1 }}>
              {hoverSnap.ghost_count} zombie{hoverSnap.ghost_count !== 1 ? 's' : ''}
            </span>
          )}
        </div>
      )}
    </div>
  );
}
