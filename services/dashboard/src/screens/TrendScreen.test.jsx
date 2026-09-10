// Regression coverage for the day-bucketing bugs found while investigating
// the Trend screen's "Avg zombie monthly-rate" headline reading way higher
// than reality:
//
// 1. total_monthly_cost is a point-in-time RATE ("if this keeps up for a
//    month"), not a per-scan charge, so bucketing by day must take one
//    *reading* per account per day (the latest), not sum every scan that
//    happened to land on that day.
// 2. The day a scan belongs to must be computed in the viewer's LOCAL
//    timezone, not the UTC date the timestamp string starts with -- two
//    scans a viewer sees as two different calendar days can share a UTC
//    date if local time is far enough ahead of UTC.
//
// The whole file runs pinned to UTC by default so bucket-boundary tests are
// deterministic regardless of the host machine's configured timezone; the
// "UTC-vs-local" describe blocks below explicitly opt into a non-UTC zone
// to prove the local-time fix itself works.
import { describe, it, expect, beforeAll, afterAll, beforeEach, afterEach } from 'vitest';
import { latestPerAccountPerDay, aggregateToDays, localDayKey } from './TrendScreen';

function snap(account_id, snapshot_at, total_monthly_cost, zombie_count = 1) {
  return { account_id, snapshot_at, total_monthly_cost, zombie_count };
}

// Assigning `undefined` to process.env.TZ would coerce to the string
// "undefined" instead of clearing the var -- delete outright so an
// originally-unset TZ stays unset.
function withTZ(zone) {
  let original;
  beforeEach(() => {
    original = process.env.TZ;
    process.env.TZ = zone;
  });
  afterEach(() => {
    if (original === undefined) delete process.env.TZ;
    else process.env.TZ = original;
  });
}

let originalTZ;
beforeAll(() => { originalTZ = process.env.TZ; process.env.TZ = 'UTC'; });
afterAll(() => {
  if (originalTZ === undefined) delete process.env.TZ;
  else process.env.TZ = originalTZ;
});

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
    // Hours 15-22 stay well clear of both UTC-day edges.
    const snaps = Array.from({ length: 8 }, (_, i) =>
      snap('acct-1', `2026-09-06T${String(15 + i).padStart(2, '0')}:00:00Z`, 3.6)
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

describe('localDayKey (UTC-vs-local timezone boundary)', () => {
  withTZ('Etc/GMT-2'); // fixed UTC+2, no DST

  it("buckets by the viewer's local calendar day, not the UTC date", () => {
    // 2026-09-09T23:25:00Z is 2026-09-10 01:25 local at UTC+2 -- the exact
    // shape of the real bug: a scan the user sees as "10 Sept" on their
    // screen landing in the "9 Sept" bucket because the code sliced the
    // UTC date instead of converting to local time first.
    expect(localDayKey('2026-09-09T23:25:00Z')).toBe('2026-09-10');
    expect(localDayKey('2026-09-09T09:02:00Z')).toBe('2026-09-09');
  });
});

describe('aggregateToDays (UTC-vs-local timezone boundary)', () => {
  withTZ('Etc/GMT-2');

  it('does not merge two different local calendar days that share a UTC date', () => {
    const snaps = [
      snap('acct-1', '2026-09-09T09:02:00Z', 3.6), // 9 Sept 11:02 local
      snap('acct-1', '2026-09-09T23:25:00Z', 3.6), // 10 Sept 01:25 local
    ];

    const days = aggregateToDays(snaps);

    // Pre-fix, both shared the UTC date "2026-09-09" and were summed into
    // one $7.20 day. They're two different days locally and must stay separate.
    expect(days).toHaveLength(2);
    expect(days.map(d => d.total_monthly_cost)).toEqual([3.6, 3.6]);
  });
});
