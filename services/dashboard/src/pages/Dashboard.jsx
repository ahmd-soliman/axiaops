import { useNavigate, useSearchParams, Navigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchAccounts, setAuthToken } from '../api/client';
import { clearToken, getToken } from '../auth/storage';
import { queryClient } from '../main';
import DashboardScreen from '../screens/DashboardScreen';

function parseJwt(token) {
  try {
    return JSON.parse(atob(token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')));
  } catch {
    return {};
  }
}

export default function Dashboard() {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const selectedAccount = params.get('account');

  const accounts = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });
  const claims   = parseJwt(getToken() ?? '');
  const orgName  = claims.org_name || claims.org_code || '';

  const skipConnect = params.get('skip_connect') === '1';
  if (accounts.data?.length === 0 && !skipConnect) return <Navigate to="/connect?onboarding=1" replace />;

  return (
    <DashboardScreen
      orgName={orgName}
      accounts={accounts.data ?? []}
      selectedAccount={selectedAccount}
      onSelectAccount={(id) => id ? setParams({ account: id }) : setParams({})}
      onSelectGhost={(g) =>
        navigate(`/detail/${g.resource_id}?account=${g.internal_account_id}&region=${g.region}&service=${g.service}`)
      }
      onShowTrend={() => navigate('/trend')}
      onConnectAccount={() => navigate('/connect')}
      onEditAccount={(acc) => navigate(`/settings/${acc.id}`)}
      onDeleteAccount={() => queryClient.invalidateQueries({ queryKey: ['accounts'] })}
      onLogout={async () => {
        clearToken();
        setAuthToken(null);
        queryClient.clear();
        navigate('/login', { replace: true });
      }}
    />
  );
}
