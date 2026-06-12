import { useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchAccounts } from '../api/client';
import CostAnalyticsScreen from '../screens/CostAnalyticsScreen';
import { editAccountHref } from '../utils/links';

export default function CostAnalytics() {
  const [params, setParams] = useSearchParams();
  const selectedAccountId = params.get('account');

  const accounts = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });

  return (
    <CostAnalyticsScreen
      accounts={accounts.data ?? []}
      selectedAccount={selectedAccountId}
      onSelectAccount={(id) => id ? setParams({ account: id }) : setParams({})}
      connectHref="/connect"
      editAccountHref={editAccountHref}
    />
  );
}
