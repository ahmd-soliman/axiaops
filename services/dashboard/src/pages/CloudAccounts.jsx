import { useNavigate } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTheme } from '../theme/ThemeContext';
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
  const { theme: t, isDark } = useTheme();
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
    <div style={{ padding: isMobile ? 16 : 24, color: t.textMid }}>
      <div style={{
        display: 'flex',
        flexDirection: isMobile ? 'column' : 'row',
        alignItems: isMobile ? 'stretch' : 'center',
        marginBottom: isMobile ? 16 : 24,
        gap: 12,
      }}>
        <div style={{ flex: 1 }}>
          <h1 style={{ margin: 0, color: t.text, fontSize: 22, fontWeight: 700 }}>Cloud Accounts</h1>
          <p style={{ marginTop: 4, marginBottom: 0, color: t.textMuted, fontSize: 13 }}>
            AWS accounts AxiaOps is monitoring.
          </p>
        </div>
        {canConnect && (
          <button
            type="button"
            onClick={() => navigate('/connect')}
            style={{ ...primaryButton(t), width: isMobile ? '100%' : undefined, minHeight: isMobile ? 44 : undefined }}
          >
            + Connect Account
          </button>
        )}
      </div>

      <section
        style={{
          border: `1px solid ${t.border}`,
          borderRadius: 8,
          backgroundColor: t.surface,
          overflow: 'hidden',
        }}
      >
        {accounts.isPending ? (
          <div style={{ padding: 32, textAlign: 'center' }}><Spinner /></div>
        ) : accounts.isError ? (
          <div style={{ padding: 24, color: t.error, fontSize: 13 }}>Failed to load accounts.</div>
        ) : accounts.data?.length === 0 ? (
          <EmptyState t={t} canConnect={canConnect} onConnect={() => navigate('/connect')} />
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
                      <span style={{ fontSize: 14, fontWeight: 700, color: t.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0 }}>
                        {a.label || '—'}
                      </span>
                      <StatusBadge t={t} isDark={isDark} status={a.status} />
                    </>
                  }
                  body={
                    <>
                      {a.account_id && (
                        <span style={{ fontFamily: '"Geist Mono Variable", monospace', fontSize: 12, color: t.textMid, wordBreak: 'break-all' }}>
                          {a.account_id}
                        </span>
                      )}
                      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', fontSize: 12, color: t.textMuted }}>
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
                          style={{ ...ghostButton(t, isScanning), flex: 1, minHeight: 40 }}
                        >
                          {isScanning ? 'Scanning…' : 'Scan'}
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={() => navigate(`/settings/cloud-accounts/${a.id}`)}
                        aria-label="Manage account"
                        style={{ ...ghostButton(t), flex: 1, minHeight: 40 }}
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
              <tr style={{ borderBottom: `1px solid ${t.border}`, backgroundColor: t.surfaceRaised }}>
                <Th t={t}>Label</Th>
                <Th t={t}>AWS Account</Th>
                <Th t={t}>Region</Th>
                <Th t={t}>Status</Th>
                <Th t={t}>Last Scan</Th>
                <Th t={t} align="right">Actions</Th>
              </tr>
            </thead>
            <tbody>
              {(accounts.data || []).map((a) => (
                <tr
                  key={a.id}
                  style={{ borderBottom: `1px solid ${t.border}`, cursor: 'pointer' }}
                  onClick={() => navigate(`/settings/cloud-accounts/${a.id}`)}
                >
                  <Td t={t}><span style={{ color: t.text, fontWeight: 600 }}>{a.label || '—'}</span></Td>
                  <Td t={t} mono>{a.account_id || '—'}</Td>
                  <Td t={t}>{a.region}</Td>
                  <Td t={t}><StatusBadge t={t} isDark={isDark} status={a.status} /></Td>
                  <Td t={t}>{formatRelative(a.last_scanned_at)}</Td>
                  <Td t={t} align="right">
                    <div style={{ display: 'inline-flex', gap: 6 }} onClick={(e) => e.stopPropagation()}>
                      {canScan && (
                        <button
                          type="button"
                          onClick={() => scanMutation.mutate(a.id)}
                          disabled={a.status === 'scanning'}
                          style={ghostButton(t, a.status === 'scanning')}
                        >
                          {a.status === 'scanning' ? 'Scanning…' : 'Scan'}
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={() => navigate(`/settings/cloud-accounts/${a.id}`)}
                        aria-label="Manage account"
                        title="Manage"
                        style={ghostButton(t)}
                      >
                        ⚙
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

function EmptyState({ t, canConnect, onConnect }) {
  return (
    <div style={{ padding: 48, textAlign: 'center' }}>
      <p style={{ marginTop: 0, marginBottom: 16, fontSize: 14, color: t.textMid }}>
        No cloud accounts connected yet.
      </p>
      {canConnect && (
        <button type="button" onClick={onConnect} style={primaryButton(t)}>
          + Connect Your First Account
        </button>
      )}
    </div>
  );
}

function StatusBadge({ t, isDark, status }) {
  const palette = {
    connected:            { fg: '#10b981', bg: isDark ? 'rgba(16,185,129,0.15)' : '#d1fae5' },
    scanning:             { fg: '#3b82f6', bg: isDark ? 'rgba(59,130,246,0.15)' : '#dbeafe' },
    error:                { fg: '#ef4444', bg: isDark ? 'rgba(239,68,68,0.15)' : '#fee2e2' },
    scan_timeout:         { fg: '#f59e0b', bg: isDark ? 'rgba(245,158,11,0.15)' : '#fef3c7' },
    circuit_breaker_open: { fg: '#f59e0b', bg: isDark ? 'rgba(245,158,11,0.15)' : '#fef3c7' },
  }[status] || { fg: t.textMuted, bg: t.surfaceRaised };
  return (
    <span style={{
      display: 'inline-block',
      padding: '2px 8px',
      borderRadius: 4,
      fontSize: 11,
      fontWeight: 600,
      color: palette.fg,
      backgroundColor: palette.bg,
      letterSpacing: 0.2,
    }}>
      {STATUS_LABEL[status] ?? status ?? 'Unknown'}
    </span>
  );
}

function Th({ t, children, align }) {
  return (
    <th style={{
      padding: '10px 12px',
      textAlign: align || 'left',
      fontWeight: 600,
      fontSize: 12,
      color: t.textMuted,
      letterSpacing: 0.3,
    }}>{children}</th>
  );
}

function Td({ t, children, align, mono }) {
  return (
    <td style={{
      padding: '10px 12px',
      color: t.text,
      textAlign: align || 'left',
      fontFamily: mono ? '"Geist Mono Variable", monospace' : undefined,
      fontSize: mono ? 12 : 13,
    }}>{children}</td>
  );
}

function primaryButton(t) {
  return {
    padding: '8px 14px',
    border: 'none',
    borderRadius: 6,
    backgroundColor: t.accent,
    color: '#fff',
    fontWeight: 600,
    fontSize: 13,
    cursor: 'pointer',
    flexShrink: 0,
  };
}

function ghostButton(t, disabled) {
  return {
    padding: '5px 10px',
    border: `1px solid ${t.border}`,
    borderRadius: 6,
    backgroundColor: 'transparent',
    color: t.text,
    fontSize: 12,
    fontWeight: 600,
    cursor: disabled ? 'not-allowed' : 'pointer',
    opacity: disabled ? 0.55 : 1,
  };
}
