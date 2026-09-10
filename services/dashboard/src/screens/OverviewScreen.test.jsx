// Regression coverage for the Monthly Waste card's delta badge showing a
// nonsensical percentage (e.g. "▲ 144000.0%") -- the same bug class already
// fixed on TrendScreen.jsx (#80/#81), duplicated here because this file has
// its own separate day roll-up (computeDailyTotals) feeding its own delta
// calculation. Two causes, both covered below:
//
// 1. Same-account same-day rescans were summed instead of deduped (a rate
//    counted N times instead of once).
// 2. Day bucketing sliced the raw UTC timestamp instead of converting to
//    the viewer's local calendar day first.
import { describe, it, expect, beforeAll, afterAll } from 'vitest';
import { computeDailyTotals } from './OverviewScreen';

function snap(account_id, snapshot_at, total_monthly_cost) {
  return { account_id, snapshot_at, total_monthly_cost };
}

// Pin to UTC by default so bucket-boundary assertions are deterministic
// regardless of the host machine's configured timezone.
let originalTZ;
beforeAll(() => { originalTZ = process.env.TZ; process.env.TZ = 'UTC'; });
afterAll(() => {
  if (originalTZ === undefined) delete process.env.TZ;
  else process.env.TZ = originalTZ;
});

describe('computeDailyTotals', () => {
  it('does not inflate a day where one account rescanned multiple times', () => {
    const rows = [
      snap('acct-1', '2026-09-06T15:00:00Z', 7.2),
      snap('acct-1', '2026-09-06T18:00:00Z', 7.2),
      snap('acct-1', '2026-09-06T21:00:00Z', 7.2),
    ];

    const totals = computeDailyTotals(rows, 30, null);

    expect(totals).toHaveLength(1);
    expect(totals[0][1]).toBe(7.2); // not 21.6
  });

  it('sums correctly across distinct accounts on the same day', () => {
    const rows = [
      snap('acct-1', '2026-09-06T10:00:00Z', 3.6),
      snap('acct-2', '2026-09-06T11:00:00Z', 1.2),
    ];

    const totals = computeDailyTotals(rows, 30, null);

    expect(totals).toHaveLength(1);
    expect(totals[0][1]).toBe(4.8);
  });

  it('trims to the trailing period window', () => {
    const rows = [
      snap('acct-1', '2026-08-01T10:00:00Z', 1.0),
      snap('acct-1', '2026-09-01T10:00:00Z', 2.0),
      snap('acct-1', '2026-09-02T10:00:00Z', 3.0),
    ];

    const totals = computeDailyTotals(rows, 2, null);

    expect(totals).toHaveLength(2);
    expect(totals.map(([, cost]) => cost)).toEqual([2.0, 3.0]);
  });

  it('scopes to an explicit custom range instead of trailing-N', () => {
    const rows = [
      snap('acct-1', '2026-08-01T10:00:00Z', 1.0),
      snap('acct-1', '2026-09-01T10:00:00Z', 2.0),
      snap('acct-1', '2026-09-02T10:00:00Z', 3.0),
    ];

    const totals = computeDailyTotals(rows, 30, { sinceIso: '2026-09-01', untilIso: '2026-09-01' });

    expect(totals).toHaveLength(1);
    expect(totals[0][1]).toBe(2.0);
  });
});

describe('computeDailyTotals (UTC-vs-local timezone boundary)', () => {
  let original;
  beforeAll(() => { original = process.env.TZ; process.env.TZ = 'Etc/GMT-2'; }); // fixed UTC+2, no DST
  afterAll(() => {
    if (original === undefined) delete process.env.TZ;
    else process.env.TZ = original;
  });

  it('does not merge two different local calendar days that share a UTC date', () => {
    const rows = [
      snap('acct-1', '2026-09-09T09:02:00Z', 3.6), // 9 Sept 11:02 local
      snap('acct-1', '2026-09-09T23:25:00Z', 3.6), // 10 Sept 01:25 local
    ];

    const totals = computeDailyTotals(rows, 30, null);

    // Pre-fix, both shared the UTC date "2026-09-09" and were summed into
    // one $7.20 day, which is exactly the shape that produced the
    // nonsensical "▲ 144000.0%" delta against an earlier $0 day.
    expect(totals).toHaveLength(2);
    expect(totals.map(([, cost]) => cost)).toEqual([3.6, 3.6]);
  });
});
