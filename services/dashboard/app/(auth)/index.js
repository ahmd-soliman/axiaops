import React from 'react';
import { Redirect, useRouter, useLocalSearchParams } from 'expo-router';
import { useQuery } from '@tanstack/react-query';

import DashboardScreen from '../../src/screens/DashboardScreen';
import { fetchAccounts, setAuthToken } from '../../src/api/client';
import { clearToken, getToken } from '../../src/auth/storage';
import { queryClient } from '../_layout';

// Decode the org_name claim from a JWT without verifying the signature.
function parseJwt(token) {
  try {
    const base64 = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
    return JSON.parse(atob(base64));
  } catch {
    return {};
  }
}

export default function Dashboard() {
  const router = useRouter();

  // account query param — null means "all accounts".
  const { account } = useLocalSearchParams();
  const selectedAccount = account ?? null;

  // Fetch accounts (cached by React Query).
  const accounts = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });

  // Derive org name from any resolved token already in the API client,
  // falling back to storage on a hard refresh.
  const [orgName, setOrgName] = React.useState('');
  React.useEffect(() => {
    getToken().then((tok) => {
      if (!tok) return;
      const claims = parseJwt(tok);
      setOrgName(claims.org_name || claims.org_code || '');
    });
  }, []);

  // Zero-account guard — redirect to /connect until the first account is added.
  if (accounts.data && accounts.data.length === 0) {
    return <Redirect href="/connect" />;
  }

  function handleSelectAccount(id) {
    if (id) {
      router.setParams({ account: id });
    } else {
      // Clear the param when user selects "All Accounts".
      router.setParams({ account: undefined });
    }
  }

  async function handleLogout() {
    await clearToken();
    setAuthToken(null);
    queryClient.clear();
    router.replace('/login');
  }

  return (
    <DashboardScreen
      orgName={orgName}
      accounts={accounts.data ?? []}
      selectedAccount={selectedAccount}
      onSelectAccount={handleSelectAccount}
      onSelectGhost={(ghost) =>
        router.push({
          pathname: '/detail/[id]',
          params: {
            id: ghost.resource_id,
            account: ghost.internal_account_id,
            region: ghost.region,
            service: ghost.service,
          },
        })
      }
      onShowTrend={() => router.push('/trend')}
      onConnectAccount={() => router.push('/connect')}
      onEditAccount={(acc) => router.push(`/settings/${acc.id}`)}
      onDeleteAccount={() => {
        queryClient.invalidateQueries({ queryKey: ['accounts'] });
      }}
      onLogout={handleLogout}
    />
  );
}
