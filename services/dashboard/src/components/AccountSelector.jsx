import { useState } from 'react';
import { useTheme } from '../theme/ThemeContext';
import { Overlay } from '../components/primitives';
import { Spinner } from '../components/primitives';

const STATUS_LABEL = {
  connected:            'Connected',
  scanning:             'Scanning…',
  error:                'Error',
  scan_timeout:         'Timed out',
  circuit_breaker_open: 'Unavailable',
};

export default function AccountSelector({
  accounts,
  selectedAccount,
  onSelectAccount,
  onConnectAccount,
  onEditAccount,
  onScanAccount,
}) {
  const { theme } = useTheme();
  const [showDropdown, setShowDropdown] = useState(false);

  const currentAccount = selectedAccount ? accounts.find((acc) => acc.id === selectedAccount) : null;
  const displayText = selectedAccount === null
    ? 'All Accounts'
    : (currentAccount?.label || currentAccount?.access_key_id.slice(0, 8) + '…');

  const getStatusColor = (account) => {
    if (account.status === 'error') return theme.error;
    if (account.status === 'scan_timeout' || account.status === 'circuit_breaker_open') return theme.warning;
    return theme.success;
  };

  const s = {
    selector: { display: 'flex', flexDirection: 'row', alignItems: 'center', backgroundColor: theme.surfaceRaised, paddingLeft: 12, paddingRight: 12, paddingTop: 6, paddingBottom: 6, borderRadius: 6, minWidth: 120, maxWidth: 180, cursor: 'pointer', border: `1px solid ${theme.border}` },
    selectorText: { color: theme.text, fontSize: 13, fontWeight: 600, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
    chevron: { color: theme.textMuted, fontSize: 10, marginLeft: 6 },
    dropdown: { backgroundColor: theme.surface, borderRadius: 12, padding: 16, margin: 20, maxHeight: '70vh', overflowY: 'auto', minWidth: 300, maxWidth: 400 },
    dropdownTitle: { fontSize: 16, fontWeight: 700, color: theme.text, marginBottom: 12, display: 'block' },
    accountItem: { display: 'flex', flexDirection: 'row', alignItems: 'center', paddingTop: 12, paddingBottom: 12, paddingLeft: 8, paddingRight: 8, borderRadius: 8, gap: 10, cursor: 'pointer', border: 'none', background: 'none', width: '100%', textAlign: 'left' },
    statusDot: { width: 8, height: 8, borderRadius: '50%', flexShrink: 0 },
    accountInfo: { flex: 1, display: 'flex', flexDirection: 'column' },
    accountName: { fontSize: 14, fontWeight: 600, color: theme.text },
    accountDetail: { fontSize: 12, color: theme.textMuted, marginTop: 2 },
    accountActions: { display: 'flex', flexDirection: 'row', gap: 4 },
    actionBtn: { paddingLeft: 8, paddingRight: 8, paddingTop: 4, paddingBottom: 4, borderRadius: 4, backgroundColor: theme.surfaceRaised, border: 'none', cursor: 'pointer' },
    actionText: { fontSize: 12, color: theme.accent, fontWeight: 600 },
    connectBtn: { marginTop: 12, paddingTop: 10, paddingBottom: 10, display: 'flex', alignItems: 'center', justifyContent: 'center', borderTop: `1px solid ${theme.border}`, background: 'none', border: 'none', borderTopWidth: 1, borderTopStyle: 'solid', borderTopColor: theme.border, cursor: 'pointer', width: '100%' },
    connectText: { color: theme.accent, fontSize: 14, fontWeight: 600 },
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
            style={{ ...s.accountItem, backgroundColor: selectedAccount === null ? theme.accentLight : 'transparent' }}
            onClick={() => { onSelectAccount(null); setShowDropdown(false); }}
          >
            <div style={s.accountInfo}>
              <span style={{ ...s.accountName, color: selectedAccount === null ? theme.accentText : theme.text }}>All Accounts</span>
              <span style={s.accountDetail}>View all resources</span>
            </div>
          </button>

          {accounts.map((account) => {
            const isActive = selectedAccount === account.id;
            return (
              <button
                key={account.id}
                style={{ ...s.accountItem, backgroundColor: isActive ? theme.accentLight : 'transparent' }}
                onClick={() => { onSelectAccount(account.id); setShowDropdown(false); }}
              >
                <div style={{ ...s.statusDot, backgroundColor: getStatusColor(account) }} />
                <div style={s.accountInfo}>
                  <span style={{ ...s.accountName, color: isActive ? theme.accentText : theme.text }}>
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
                    {account.status === 'scanning' ? <Spinner size={14} color={theme.accent} /> : <span style={s.actionText}>Scan</span>}
                  </button>
                  <button
                    style={s.actionBtn}
                    onClick={(e) => { e.stopPropagation(); onEditAccount(account); setShowDropdown(false); }}
                  >
                    <span style={s.actionText}>⚙</span>
                  </button>
                </div>
              </button>
            );
          })}

          {onConnectAccount && (
            <button style={s.connectBtn} onClick={() => { onConnectAccount(); setShowDropdown(false); }}>
              <span style={s.connectText}>+ Connect New Account</span>
            </button>
          )}
        </div>
      </Overlay>
    </div>
  );
}
