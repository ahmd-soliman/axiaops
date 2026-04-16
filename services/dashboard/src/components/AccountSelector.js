import React, { useState } from 'react';
import { View, Text, TouchableOpacity, Modal, FlatList, ActivityIndicator, StyleSheet } from 'react-native';
import { useTheme } from '../theme/ThemeContext';

export default function AccountSelector({ 
  accounts, 
  selectedAccount, 
  onSelectAccount, 
  onConnectAccount, 
  onEditAccount, 
  onScanAccount,
  scanning 
}) {
  const { theme } = useTheme();
  const [showDropdown, setShowDropdown] = useState(false);

  const currentAccount = selectedAccount 
    ? accounts.find(acc => acc.id === selectedAccount)
    : null;

  const displayText = selectedAccount === null 
    ? 'All Accounts'
    : (currentAccount?.label || currentAccount?.access_key_id.slice(0, 8) + '…');

  function handleAccountSelect(accountId) {
    onSelectAccount(accountId);
    setShowDropdown(false);
  }

  function handleScan(account, event) {
    event.stopPropagation();
    onScanAccount(account.id);
    setShowDropdown(false);
  }

  function handleSettings(account, event) {
    event.stopPropagation();
    onEditAccount(account);
    setShowDropdown(false);
  }

  function handleConnect() {
    onConnectAccount();
    setShowDropdown(false);
  }

  const getStatusColor = (account) => {
    if (account.status === 'error') return theme.error;
    if (account.status === 'scan_timeout' || account.status === 'circuit_breaker_open') return theme.warning;
    return theme.success;
  };

  const styles = createStyles(theme);

  return (
    <View style={styles.container}>
      <TouchableOpacity 
        style={styles.selector} 
        onPress={() => setShowDropdown(true)}
      >
        <Text style={styles.selectorText} numberOfLines={1}>
          {displayText}
        </Text>
        <Text style={styles.chevron}>▼</Text>
      </TouchableOpacity>

      <Modal
        visible={showDropdown}
        transparent
        animationType="fade"
        onRequestClose={() => setShowDropdown(false)}
      >
        <TouchableOpacity 
          style={styles.overlay} 
          activeOpacity={1} 
          onPress={() => setShowDropdown(false)}
        >
          <View style={styles.dropdown}>
            <Text style={styles.dropdownTitle}>Switch Account</Text>
            
            <TouchableOpacity
              style={[styles.accountItem, selectedAccount === null && styles.accountItemActive]}
              onPress={() => handleAccountSelect(null)}
            >
              <View style={styles.accountInfo}>
                <Text style={styles.accountName}>All Accounts</Text>
                <Text style={styles.accountDetail}>View all resources</Text>
              </View>
            </TouchableOpacity>

            <FlatList
              data={accounts}
              keyExtractor={(item) => item.id}
              renderItem={({ item: account }) => (
                <TouchableOpacity
                  style={[styles.accountItem, selectedAccount === account.id && styles.accountItemActive]}
                  onPress={() => handleAccountSelect(account.id)}
                >
                  <View style={[styles.statusDot, { backgroundColor: getStatusColor(account) }]} />
                  <View style={styles.accountInfo}>
                    <Text style={styles.accountName}>
                      {account.label || account.access_key_id.slice(0, 8) + '…'}
                    </Text>
                    <Text style={styles.accountDetail}>
                      {account.region} • {account.status === 'connected' ? 'Connected' : 'Error'}
                    </Text>
                  </View>
                  <View style={styles.accountActions}>
                    <TouchableOpacity
                      style={styles.actionBtn}
                      onPress={(e) => handleScan(account, e)}
                      disabled={scanning === account.id}
                    >
                      {scanning === account.id ? (
                        <ActivityIndicator size="small" color={theme.accent} />
                      ) : (
                        <Text style={styles.actionText}>Scan</Text>
                      )}
                    </TouchableOpacity>
                    <TouchableOpacity
                      style={styles.actionBtn}
                      onPress={(e) => handleSettings(account, e)}
                    >
                      <Text style={styles.actionText}>⚙</Text>
                    </TouchableOpacity>
                  </View>
                </TouchableOpacity>
              )}
            />

            <TouchableOpacity style={styles.connectBtn} onPress={handleConnect}>
              <Text style={styles.connectText}>+ Connect New Account</Text>
            </TouchableOpacity>
          </View>
        </TouchableOpacity>
      </Modal>
    </View>
  );
}

const createStyles = (theme) => StyleSheet.create({
  container: {
    position: 'relative',
  },
  selector: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: theme.navyMid,
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 6,
    minWidth: 120,
    maxWidth: 180,
  },
  selectorText: {
    color: theme.white,
    fontSize: 13,
    fontWeight: '600',
    flex: 1,
  },
  chevron: {
    color: theme.textSub,
    fontSize: 10,
    marginLeft: 6,
  },
  overlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.5)',
    justifyContent: 'center',
    alignItems: 'center',
  },
  dropdown: {
    backgroundColor: theme.white,
    borderRadius: 12,
    padding: 16,
    margin: 20,
    maxHeight: '70%',
    minWidth: 300,
    maxWidth: 400,
  },
  dropdownTitle: {
    fontSize: 16,
    fontWeight: '700',
    color: theme.navy,
    marginBottom: 12,
  },
  accountItem: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingVertical: 12,
    paddingHorizontal: 8,
    borderRadius: 8,
    gap: 10,
  },
  accountItemActive: {
    backgroundColor: theme.accentLight,
  },
  statusDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  accountInfo: {
    flex: 1,
  },
  accountName: {
    fontSize: 14,
    fontWeight: '600',
    color: theme.navy,
  },
  accountDetail: {
    fontSize: 12,
    color: theme.textSub,
    marginTop: 2,
  },
  accountActions: {
    flexDirection: 'row',
    gap: 4,
  },
  actionBtn: {
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderRadius: 4,
    backgroundColor: theme.bgSecondary,
  },
  actionText: {
    fontSize: 12,
    color: theme.accent,
    fontWeight: '600',
  },
  connectBtn: {
    marginTop: 12,
    paddingVertical: 10,
    alignItems: 'center',
    borderTopWidth: 1,
    borderTopColor: theme.border,
  },
  connectText: {
    color: theme.accent,
    fontSize: 14,
    fontWeight: '600',
  },
});