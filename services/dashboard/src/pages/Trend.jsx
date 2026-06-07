import { useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchAccounts } from '../api/client';
import TrendScreen from '../screens/TrendScreen';
import { editAccountHref } from '../utils/links';

export default function Trend() {
  const [params, setParams] = useSearchParams();
  const selectedAccountId = params.get('account');

  const accounts = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });

  return (
    <TrendScreen
      accounts={accounts.data ?? []}
      selectedAccount={selectedAccountId}
      selectedAwsAccount={selectedAccountId}
      onSelectAccount={(id) => id ? setParams({ account: id }) : setParams({})}
      connectHref="/connect"
      editAccountHref={editAccountHref}
    />
  );
}
