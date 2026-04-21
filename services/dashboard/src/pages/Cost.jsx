import { useNavigate, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchAccounts } from '../api/client';
import CostScreen from '../screens/CostScreen';

export default function Cost() {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const selectedAccountId = params.get('account');

  const accounts = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });

  // Resolve system account ID: try AWS account ID first, fallback to internal UUID
  // Even while accounts are loading, pass the UUID to the API so filtering works
  const selectedAccount = selectedAccountId
    ? (accounts.data?.find(a => a.id === selectedAccountId)?.account_id || selectedAccountId)
    : null;

  return (
    <CostScreen
      accounts={accounts.data ?? []}
      selectedAccount={selectedAccountId}
      selectedAwsAccount={selectedAccount}
      onSelectAccount={(id) => id ? setParams({ account: id }) : setParams({})}
      onConnectAccount={() => navigate('/connect')}
      onEditAccount={(acc) => navigate(`/settings/${acc.id}`)}
    />
  );
}
