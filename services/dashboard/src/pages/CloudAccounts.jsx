import { useNavigate } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useMe } from '../context/MeContext';
import { useToast } from '../context/ToastContext';
import { fetchAccounts, scanAccount } from '../api/client';
import { PERM } from '../api/permissions';
import { Spinner } from '../components/primitives';
import { useBreakpoint } from '../components/primitives/useBreakpoint';
import { CardRow } from '../components/primitives/CardRow';
import { STATUS_LABEL } from '../utils/accountStatus';
import { formatRelative } from '../utils/relativeTime';

// Top-level Cloud Accounts list. Companion to the navbar's AccountSelector,
// which exists for transient context-switching ("filter dashboard data to
// account X"). This page is the management surface — full table, sortable
// columns later, primary path for connect/edit/delete.

export default function CloudAccounts() {
  const { can } = useMe();
  const { toast } = useToast();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { isAtMost } = useBreakpoint();
  const isMobile = isAtMost('sm');

  const accounts = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });

  const scanMutation = useMutation({
    mutationFn: scanAccount,
    onSuccess: () => {
      toast('Scan started.', 'success');
      qc.invalidateQueries({ queryKey: ['accounts'] });
    },
    onError: (err) => {
      if (err.code === 'already_scanning') {
        toast('Scan already in progress.', 'warning');
        return;
      }
      // B1.6 slice 8 — license scan-gate (plan §4.9.2b).
      if (err.code === 'license_expired') {
        toast('License expired — scans paused. Contact sales@axiaops.io to renew.', 'error');
        return;
      }
      toast(err.body || err.message || 'Failed to start scan.', 'error');
    },
  });

  const canConnect = can(PERM.ACCOUNTS_WRITE);
  const canScan = can(PERM.ACCOUNTS_SCAN);

  return (
    <div style={{ padding: isMobile ? 16 : 24, color: 'var(--color-text-mid)' }}>
      <div style={{
        display: 'flex',
        flexDirection: isMobile ? 'column' : 'row',
        alignItems: isMobile ? 'stretch' : 'center',
        marginBottom: isMobile ? 16 : 24,
        gap: 12,
      }}>
        <div style={{ flex: 1 }}>
          <h1 style={{ margin: 0, color: 'var(--color-text)', fontSize: 22, fontWeight: 700 }}>Cloud Accounts</h1>
          <p style={{ marginTop: 4, marginBottom: 0, color: 'var(--color-text-muted)', fontSize: 13 }}>
            AWS accounts AxiaOps is monitoring.
          </p>
        </div>
        {canConnect && (
          <button
            type="button"
            onClick={() => navigate('/connect')}
            style={{ ...primaryButton(), width: isMobile ? '100%' : undefined, minHeight: isMobile ? 44 : undefined }}
          >
            + Connect Account
          </button>
        )}
      </div>

      <section
        style={{
          border: `1px solid var(--color-border)`,
          borderRadius: 8,
          backgroundColor: 'var(--color-surface)',
          overflow: 'hidden',
        }}
      >
        {accounts.isPending ? (
          <div style={{ padding: 32, textAlign: 'center' }}><Spinner /></div>
        ) : accounts.isError ? (
          <div style={{ padding: 24, color: 'var(--color-error)', fontSize: 13 }}>Failed to load accounts.</div>
        ) : accounts.data?.length === 0 ? (
          <EmptyState canConnect={canConnect} onConnect={() => navigate('/connect')} />
        ) : isMobile ? (
          // Phone layout — six-column <table> doesn't reflow (Label, AWS
          // Account, Region, Status, Last Scan, Actions). Cards keep every
          // field accessible without horizontal page scroll. Tapping the
          // card body navigates to the management page (same as desktop
          // row click); action buttons live on their own row inside each
          // card with stopPropagation so they don't trigger the body nav.
          <div style={{ padding: 12, display: 'flex', flexDirection: 'column', gap: 8 }}>
            {(accounts.data || []).map((a) => {
              const isScanning = a.status === 'scanning';
              return (
                <CardRow
                  key={a.id}
                  onClick={() => navigate(`/settings/cloud-accounts/${a.id}`)}
                  header={
                    <>
                      <span style={{ fontSize: 14, fontWeight: 700, color: 'var(--color-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0 }}>
                        {a.label || '—'}
                      </span>
                      <StatusBadge status={a.status} />
                    </>
                  }
                  body={
                    <>
                      {a.account_id && (
                        <span style={{ fontFamily: '"Geist Mono Variable", monospace', fontSize: 12, color: 'var(--color-text-mid)', wordBreak: 'break-all' }}>
                          {a.account_id}
                        </span>
                      )}
                      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', fontSize: 12, color: 'var(--color-text-muted)' }}>
                        <span>{a.region}</span>
                        {a.last_scanned_at && (
                          <>
                            <span aria-hidden>·</span>
                            <span>Last scan {formatRelative(a.last_scanned_at)}</span>
                          </>
                        )}
                      </div>
                    </>
                  }
                  actions={
                    <div
                      style={{ display: 'flex', gap: 8, width: '100%' }}
                      onClick={(e) => e.stopPropagation()}
                      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') e.stopPropagation(); }}
                      role="presentation"
                    >
                      {canScan && (
                        <button
                          type="button"
                          onClick={() => scanMutation.mutate(a.id)}
                          disabled={isScanning}
                          style={{ ...ghostButton(isScanning), flex: 1, minHeight: 40 }}
                        >
                          {isScanning ? 'Scanning…' : 'Scan'}
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={() => navigate(`/settings/cloud-accounts/${a.id}`)}
                        aria-label={`Manage ${a.label || 'account'}`}
                        style={{ ...ghostButton(), flex: 1, minHeight: 40 }}
                      >
                        Manage
                      </button>
                    </div>
                  }
                />
              );
            })}
          </div>
        ) : (
          <table aria-label="Connected cloud accounts" style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr style={{ borderBottom: `1px solid var(--color-border)`, backgroundColor: 'var(--color-surface-raised)' }}>
                <Th>Label</Th>
                <Th>AWS Account</Th>
                <Th>Region</Th>
                <Th>Status</Th>
                <Th>Last Scan</Th>
                <Th align="right">Actions</Th>
              </tr>
            </thead>
            <tbody>
              {(accounts.data || []).map((a) => (
                <tr
                  key={a.id}
                  style={{ borderBottom: `1px solid var(--color-border)`, cursor: 'pointer' }}
                  // Row click is a mouse convenience only. The keyboard /
                  // assistive-tech path is the focusable per-row Manage
                  // button below — a <tr>'s implicit `row` role isn't an
                  // interactive role, so making it tabbable would expose a
                  // handler screen readers never fire. Keeping the accessible
                  // action on a real <button> is the idiomatic pattern.
                  onClick={() => navigate(`/settings/cloud-accounts/${a.id}`)}
                >
                  <Td><span style={{ color: 'var(--color-text)', fontWeight: 600 }}>{a.label || '—'}</span></Td>
                  <Td mono>{a.account_id || '—'}</Td>
                  <Td>{a.region}</Td>
                  <Td><StatusBadge status={a.status} /></Td>
                  <Td>{formatRelative(a.last_scanned_at)}</Td>
                  <Td align="right">
                    <div style={{ display: 'inline-flex', gap: 6 }} onClick={(e) => e.stopPropagation()}>
                      {canScan && (
                        <button
                          type="button"
                          onClick={() => scanMutation.mutate(a.id)}
                          disabled={a.status === 'scanning'}
                          style={ghostButton(a.status === 'scanning')}
                        >
                          {a.status === 'scanning' ? 'Scanning…' : 'Scan'}
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={() => navigate(`/settings/cloud-accounts/${a.id}`)}
                        aria-label={`Manage ${a.label || 'account'}`}
                        title="Manage"
                        style={{ ...ghostButton(), display: 'inline-flex', alignItems: 'center' }}
                      >
                        <IconGear />
                      </button>
                    </div>
                  </Td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}

function EmptyState({ canConnect, onConnect }) {
  return (
    <div style={{ padding: 48, textAlign: 'center' }}>
      <p style={{ marginTop: 0, marginBottom: 16, fontSize: 14, color: 'var(--color-text-mid)' }}>
        No cloud accounts connected yet.
      </p>
      {canConnect && (
        <button type="button" onClick={onConnect} style={primaryButton()}>
          + Connect Your First Account
        </button>
      )}
    </div>
  );
}

// IconGear — settings/manage glyph. Replaces the bare ⚙ emoji, which
// renders inconsistently across OSes and ignores the theme; this SVG
// inherits currentColor so it tracks the ghost button's text color.
function IconGear({ size = 15 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
    </svg>
  );
}

function StatusBadge({ status }) {
  // Inline colored label — no pill chrome. Color carries the state cue.
  // Semantic design tokens (not raw hex) so the cues stay on-palette and
  // hit the dark-theme contrast targets defined in tokens.css.
  const fg = {
    connected:            'var(--color-success)',
    scanning:             'var(--color-info)',
    error:                'var(--color-error)',
    scan_timeout:         'var(--color-warning)',
    circuit_breaker_open: 'var(--color-warning)',
  }[status] || 'var(--color-text-muted)';
  return (
    <span style={{
      fontSize: 11,
      fontWeight: 600,
      color: fg,
      letterSpacing: 0.2,
    }}>
      {STATUS_LABEL[status] ?? status ?? 'Unknown'}
    </span>
  );
}

function Th({ children, align }) {
  return (
    <th style={{
      padding: '10px 12px',
      textAlign: align || 'left',
      fontWeight: 600,
      fontSize: 12,
      color: 'var(--color-text-muted)',
      letterSpacing: 0.3,
    }}>{children}</th>
  );
}

function Td({ children, align, mono }) {
  return (
    <td style={{
      padding: '10px 12px',
      color: 'var(--color-text)',
      textAlign: align || 'left',
      fontFamily: mono ? '"Geist Mono Variable", monospace' : undefined,
      fontSize: mono ? 12 : 13,
    }}>{children}</td>
  );
}

function primaryButton() {
  return {
    padding: '8px 14px',
    border: 'none',
    borderRadius: 6,
    backgroundColor: 'var(--color-accent)',
    color: 'var(--color-text-on-dark)',
    fontWeight: 600,
    fontSize: 13,
    cursor: 'pointer',
    flexShrink: 0,
  };
}

function ghostButton(disabled) {
  return {
    padding: '5px 10px',
    border: `1px solid var(--color-border)`,
    borderRadius: 6,
    backgroundColor: 'transparent',
    color: 'var(--color-text)',
    fontSize: 12,
    fontWeight: 600,
    cursor: disabled ? 'not-allowed' : 'pointer',
    opacity: disabled ? 0.55 : 1,
  };
}
