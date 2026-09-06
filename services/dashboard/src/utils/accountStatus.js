// Display labels for the cloud-account `status` field. Mirrors the values
// the API returns (services/shared/model/account.go); kept on the dashboard
// side because the strings are user-facing and may evolve independently of
// the wire format.
export const STATUS_LABEL = {
  connected:            'Connected',
  scanning:             'Scanning…',
  error:                'Error',
  scan_timeout:         'Timed Out',
  circuit_breaker_open: 'Unavailable',
  pending_cur_delivery: 'Awaiting First Delivery',
};

// Longer explanation for statuses whose label alone reads as "broken" or
// unclear — surfaced as a title/tooltip next to the label. AWS's Cost and
// Usage Report export can take up to ~24h to deliver its first data after
// being provisioned; until then a scan finding nothing is expected, not an
// error (see the ingestion scheduler's isAccountOverdue, which delays that
// first scan rather than firing it immediately against an empty bucket).
export const STATUS_HINT = {
  pending_cur_delivery: 'Billing export provisioned — first cost data typically arrives within 24 hours.',
};
