import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Overlay } from '../components/primitives';
import { Spinner } from '../components/primitives';
import { STATUS_LABEL } from '../utils/accountStatus';

export default function AccountSelector({
  accounts,
  selectedAccount,
  onSelectAccount,
  connectHref,
  editAccountHref,
  onScanAccount,
}) {
  const [showDropdown, setShowDropdown] = useState(false);

  const currentAccount = selectedAccount ? accounts.find((acc) => acc.id === selectedAccount) : null;
  const displayText = selectedAccount === null
    ? 'All Accounts'
    : (currentAccount?.label || currentAccount?.access_key_id.slice(0, 8) + '…');

  // Status-dot tint resolves to a CSS variable (set in src/styles/tokens.css);
  // the cascade picks the right hue under light vs dark automatically.
  const getStatusColor = (account) => {
    if (account.status === 'error') return 'var(--color-error)';
    if (account.status === 'scan_timeout' || account.status === 'circuit_breaker_open') return 'var(--color-warning)';
    return 'var(--color-success)';
  };

  const s = {
    selector: { display: 'flex', flexDirection: 'row', alignItems: 'center', backgroundColor: 'var(--color-surface-raised)', paddingLeft: 12, paddingRight: 12, paddingTop: 6, paddingBottom: 6, borderRadius: 6, minWidth: 120, maxWidth: 180, cursor: 'pointer', border: '1px solid var(--color-border)' },
    selectorText: { color: 'var(--color-text)', fontSize: 13, fontWeight: 600, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
    chevron: { color: 'var(--color-text-muted)', fontSize: 10, marginLeft: 6 },
    dropdown: { backgroundColor: 'var(--color-surface)', borderRadius: 12, padding: 16, margin: 20, maxHeight: '70vh', overflowY: 'auto', minWidth: 300, maxWidth: 400 },
    dropdownTitle: { fontSize: 16, fontWeight: 700, color: 'var(--color-text)', marginBottom: 12, display: 'block' },
    accountItem: { display: 'flex', flexDirection: 'row', alignItems: 'center', paddingTop: 12, paddingBottom: 12, paddingLeft: 8, paddingRight: 8, borderRadius: 8, gap: 10, cursor: 'pointer', border: 'none', background: 'none', width: '100%', textAlign: 'left' },
    statusDot: { width: 8, height: 8, borderRadius: '50%', flexShrink: 0 },
    accountInfo: { flex: 1, display: 'flex', flexDirection: 'column' },
    accountName: { fontSize: 14, fontWeight: 600, color: 'var(--color-text)' },
    accountDetail: { fontSize: 12, color: 'var(--color-text-muted)', marginTop: 2 },
    accountActions: { display: 'flex', flexDirection: 'row', gap: 4 },
    actionBtn: { paddingLeft: 8, paddingRight: 8, paddingTop: 4, paddingBottom: 4, borderRadius: 4, backgroundColor: 'var(--color-surface-raised)', border: 'none', cursor: 'pointer' },
    actionText: { fontSize: 12, color: 'var(--color-accent)', fontWeight: 600 },
    connectBtn: { marginTop: 12, paddingTop: 10, paddingBottom: 10, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'none', border: 'none', borderTopWidth: 1, borderTopStyle: 'solid', borderTopColor: 'var(--color-border)', cursor: 'pointer', width: '100%' },
    connectText: { color: 'var(--color-accent)', fontSize: 14, fontWeight: 600 },
  };

  return (
    <div style={{ position: 'relative' }}>
      <button style={s.selector} onClick={() => setShowDropdown(true)}>
        <span style={s.selectorText}>{displayText}</span>
        <span style={s.chevron}>▼</span>
      </button>

      <Overlay visible={showDropdown} onClose={() => setShowDropdown(false)}>
        <div style={s.dropdown} onClick={(e) => e.stopPropagation()}>
          <span style={s.dropdownTitle}>Switch Account</span>

          <button
            style={{ ...s.accountItem, backgroundColor: selectedAccount === null ? 'var(--color-accent-light)' : 'transparent' }}
            onClick={() => { onSelectAccount(null); setShowDropdown(false); }}
          >
            <div style={s.accountInfo}>
              <span style={{ ...s.accountName, color: selectedAccount === null ? 'var(--color-accent-text)' : 'var(--color-text)' }}>All Accounts</span>
              <span style={s.accountDetail}>View all resources</span>
            </div>
          </button>

          {accounts.map((account) => {
            const isActive = selectedAccount === account.id;
            const select = () => { onSelectAccount(account.id); setShowDropdown(false); };
            return (
              // Outer is a div, not a button — Scan/⚙ inside are real
              // <button>s and HTML forbids button-in-button (DOM nesting
              // warning + inconsistent click behaviour across browsers).
              <div
                key={account.id}
                role="button"
                tabIndex={0}
                style={{ ...s.accountItem, backgroundColor: isActive ? 'var(--color-accent-light)' : 'transparent' }}
                onClick={select}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); select(); }
                }}
              >
                <div style={{ ...s.statusDot, backgroundColor: getStatusColor(account) }} />
                <div style={s.accountInfo}>
                  <span style={{ ...s.accountName, color: isActive ? 'var(--color-accent-text)' : 'var(--color-text)' }}>
                    {account.label || account.access_key_id.slice(0, 8) + '…'}
                  </span>
                  <span style={s.accountDetail}>
                    {account.region} • {STATUS_LABEL[account.status] ?? 'Unknown'}
                  </span>
                </div>
                <div style={s.accountActions}>
                  <button
                    style={s.actionBtn}
                    onClick={(e) => { e.stopPropagation(); onScanAccount?.(account.id); setShowDropdown(false); }}
                    disabled={account.status === 'scanning' || !onScanAccount}
                  >
                    {account.status === 'scanning' ? <Spinner size={14} color="var(--color-accent)" /> : <span style={s.actionText}>Scan</span>}
                  </button>
                  <Link
                    to={editAccountHref(account)}
                    style={{ ...s.actionBtn, display: 'inline-flex', alignItems: 'center', textDecoration: 'none' }}
                    aria-label={`Manage ${account.label || 'account'}`}
                    onClick={(e) => { e.stopPropagation(); setShowDropdown(false); }}
                  >
                    <span style={s.actionText}>⚙</span>
                  </Link>
                </div>
              </div>
            );
          })}

          {connectHref && (
            <Link
              to={connectHref}
              style={{ ...s.connectBtn, textDecoration: 'none' }}
              onClick={() => setShowDropdown(false)}
            >
              <span style={s.connectText}>+ Connect New Account</span>
            </Link>
          )}
        </div>
      </Overlay>
    </div>
  );
}
