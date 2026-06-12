import { useEffect, useRef, useCallback, useMemo } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { fetchAccounts } from '../api/client';
import { useToast } from '../context/ToastContext';

const POLL_INTERVAL_MS     = 4000;
const MAX_POLL_DURATION_MS = 2 * 60 * 1000;

// Query keys that contain post-scan data and must be refetched when a scan
// completes. `accounts` is included so the `last_scanned_at` + `status` fields
// refresh across the app.
const STALE_QUERY_KEYS = ['accounts', 'summary', 'zombies', 'resources', 'costs', 'trend', 'dismissals'];

// Worker terminal statuses other than `connected` all indicate failure, with
// varying root causes surfaced by the ingestion worker in `services/ingestion/cmd/worker.go`.
const FAILURE_LABEL = {
  error:                'Scan failed',
  scan_timeout:         'Scan timed out',
  circuit_breaker_open: 'Scan unavailable (provider rate limit or outage)',
};

// useScanStatus polls account status after a scan is triggered and shows a
// toast when the scan reaches a terminal state. Call `watch(accountId, …)`
// right after `scanAccount()` succeeds. One watcher per account — redundant
// calls are ignored. All timers are cleaned up on unmount.
export function useScanStatus() {
  const queryClient = useQueryClient();
  const { toast }   = useToast();
  const pollers     = useRef(new Map());

  useEffect(() => {
    const activePollers = pollers.current;
    return () => {
      for (const p of activePollers.values()) {
        clearInterval(p.interval);
        clearTimeout(p.timeout);
      }
      activePollers.clear();
    };
  }, []);

  const stop = useCallback((accountId) => {
    const p = pollers.current.get(accountId);
    if (!p) return;
    clearInterval(p.interval);
    clearTimeout(p.timeout);
    pollers.current.delete(accountId);
    p.onEnd?.();
  }, []);

  const refetchAll = useCallback(() => {
    for (const key of STALE_QUERY_KEYS) {
      queryClient.invalidateQueries({ queryKey: [key] });
    }
  }, [queryClient]);

  const watch = useCallback((accountId, { label, onEnd } = {}) => {
    if (pollers.current.has(accountId)) return;
    const displayLabel = label || accountId.slice(0, 8);

    const tick = async () => {
      try {
        const accounts = await fetchAccounts();
        const acc = accounts.find(a => a.id === accountId);
        if (!acc || acc.status === 'scanning') return;

        if (acc.status === 'connected') {
          toast(`Scan completed for ${displayLabel}`, 'success');
        } else {
          const reason = FAILURE_LABEL[acc.status] ?? `Scan ended with status: ${acc.status}`;
          toast(`${reason} for ${displayLabel}`, 'error');
        }
        refetchAll();
        stop(accountId);
      } catch {
        // Transient network failure — keep polling until the 2-min deadline.
      }
    };

    const interval = setInterval(tick, POLL_INTERVAL_MS);
    const timeout  = setTimeout(() => {
      toast(`Scan for ${displayLabel} is taking longer than expected`, 'warning');
      refetchAll();
      stop(accountId);
    }, MAX_POLL_DURATION_MS);

    pollers.current.set(accountId, { interval, timeout, onEnd });
  }, [toast, refetchAll, stop]);

  // Stable identity so callers can safely use `watch` as an effect/callback
  // dependency without re-subscribing every render.
  return useMemo(() => ({ watch }), [watch]);
}
