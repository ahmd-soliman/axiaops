// A cost_records row exists in two granularities for the same underlying AWS
// spend: a "general" row (no resource_id — one per service/region/day) and,
// for services that support it, "resource-level" rows (one per
// resource/day) that break that same total down further. Summing both
// together double-counts every dollar that has resource-level attribution —
// see services/ingestion/internal/provider/aws/cur/fetch.go's FetchCosts vs
// FetchResourceCosts, two separate queries over the same data at different
// GROUP BY granularity.
//
// Correct rule: if the set contains any general rows, keep only those (the
// resource-level rows are already reflected in them). If the set has been
// deliberately narrowed to resource-level rows only (e.g. a resource-type
// filter), keep those instead — there's nothing else to double count
// against. Any aggregation over cost records -- a total, a daily/monthly
// chart, a per-service breakdown -- must run over this filtered set, never
// the raw API response.
export function toAggregateCostRecords(records) {
  if (!records || records.length === 0) return [];
  const hasGeneralRows = records.some(r => !r.resource_id);
  return hasGeneralRows ? records.filter(r => !r.resource_id) : records;
}

export function sumCostRecords(records) {
  return toAggregateCostRecords(records).reduce((sum, r) => sum + (r.amount || 0), 0);
}
