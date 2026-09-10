// Regression coverage for the day-bucketing bug found while investigating
// the Trend screen's "Avg zombie monthly-rate" headline reading way higher
// than reality: total_monthly_cost is a point-in-time RATE ("if this keeps
// up for a month"), not a per-scan charge, so bucketing by day must take
// one *reading* per account per day (the latest), not sum every scan that
// happened to land on that day. Summing double-, triple-, or N-counts the
// same ongoing liability whenever an account is rescanned within one day —
// which happens routinely on a dev-cadence account, and also whenever a
// local-timezone day boundary lands two real calendar days in the same UTC
// bucket (snapshot_at is compared/sliced as UTC).
import { describe, it, expect } from 'vitest';
import { latestPerAccountPerDay, aggregateToDays } from './TrendScreen';

function snap(account_id, snapshot_at, total_monthly_cost, zombie_count = 1) {
  return { account_id, snapshot_at, total_monthly_cost, zombie_count };
}

describe('latestPerAccountPerDay', () => {
  it('keeps only the latest reading when one account scans the same day more than once', () => {
    const snaps = [
      snap('acct-1', '2026-09-08T01:45:00Z', 0.0),
      snap('acct-1', '2026-09-08T11:00:00Z', 3.6),
    ];

    const result = latestPerAccountPerDay(snaps);

    expect(result).toHaveLength(1);
    expect(result[0].total_monthly_cost).toBe(3.6);
    expect(result[0].snapshot_at).toBe('2026-09-08T11:00:00Z');
  });

  it('keeps one reading per account when two accounts scan the same day', () => {
    const snaps = [
      snap('acct-1', '2026-09-08T10:00:00Z', 3.6),
      snap('acct-2', '2026-09-08T11:00:00Z', 1.2),
    ];

    const result = latestPerAccountPerDay(snaps);

    expect(result).toHaveLength(2);
    const total = result.reduce((sum, s) => sum + s.total_monthly_cost, 0);
    expect(total).toBe(4.8);
  });

  it('does not collapse the same account scanning on two different days', () => {
    const snaps = [
      snap('acct-1', '2026-09-08T10:00:00Z', 3.6),
      snap('acct-1', '2026-09-09T10:00:00Z', 3.6),
    ];

    expect(latestPerAccountPerDay(snaps)).toHaveLength(2);
  });
});

describe('aggregateToDays', () => {
  it('does not inflate a day where one account rescanned multiple times', () => {
    // Mirrors the real bug: 8 same-day rescans of one account, all at the
    // same rate. The pre-fix version summed all 8 into that day's total.
    const snaps = Array.from({ length: 8 }, (_, i) =>
      snap('acct-1', `2026-09-06T${String(17 + i).padStart(2, '0')}:00:00Z`, 3.6)
    );

    const days = aggregateToDays(snaps);

    expect(days).toHaveLength(1);
    expect(days[0].total_monthly_cost).toBe(3.6); // not 28.8
  });

  it('still sums correctly across distinct accounts on the same day', () => {
    const snaps = [
      snap('acct-1', '2026-09-06T10:00:00Z', 3.6),
      snap('acct-2', '2026-09-06T11:00:00Z', 1.2),
    ];

    const days = aggregateToDays(snaps);

    expect(days).toHaveLength(1);
    expect(days[0].total_monthly_cost).toBe(4.8);
  });
});
