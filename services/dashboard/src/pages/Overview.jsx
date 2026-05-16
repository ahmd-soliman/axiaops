import { useNavigate, useSearchParams, Navigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchAccounts } from '../api/client';
import OverviewScreen from '../screens/OverviewScreen';
import WhatsNextPanel from '../components/onboarding/WhatsNextPanel';

export default function Overview() {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const selectedAccount = params.get('account');

  const accounts = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });

  const skipConnect = params.get('skip_connect') === '1';
  if (accounts.data?.length === 0 && !skipConnect) return <Navigate to="/connect?onboarding=1" replace />;

  return (
    <>
      <WhatsNextPanel />
      <OverviewScreen
        accounts={accounts.data ?? []}
        selectedAccount={selectedAccount}
        onSelectAccount={(id) => id ? setParams({ account: id }) : setParams({})}
        onSelectZombie={(z) =>
          navigate(`/detail/${encodeURIComponent(z.resource_id)}?account=${encodeURIComponent(z.internal_account_id)}&region=${encodeURIComponent(z.region)}&service=${encodeURIComponent(z.service)}`)
        }
        onShowTrend={() => navigate('/trend')}
        onShowCosts={() => navigate(selectedAccount ? `/cost?account=${encodeURIComponent(selectedAccount)}` : '/cost')}
        onConnectAccount={() => navigate('/connect')}
        onEditAccount={(acc) => navigate(`/cloud-accounts/${acc.id}`)}
      />
    </>
  );
}
