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
};
