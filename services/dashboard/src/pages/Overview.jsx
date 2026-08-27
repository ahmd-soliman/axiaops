import { useNavigate, useSearchParams, Navigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchAccounts } from '../api/client';
import OverviewScreen from '../screens/OverviewScreen';
import { zombieDetailHref, editAccountHref } from '../utils/links';

export default function Overview() {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const selectedAccount = params.get('account');
  // Optional deep-link from the org summary's "Waste by service" rows — seeds
  // the workbench's service filter on first load.
  const serviceFilter = params.get('service');

  const accounts = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });

  const skipConnect = params.get('skip_connect') === '1';
  if (accounts.data?.length === 0 && !skipConnect) return <Navigate to="/connect?onboarding=1" replace />;

  return (
    <OverviewScreen
      accounts={accounts.data ?? []}
      selectedAccount={selectedAccount}
      initialServiceFilter={serviceFilter}
      // Account filter toggles the in-page ?account= param (it doesn't route
      // to a new screen), so it stays an imperative callback, not an href.
      onSelectAccount={(id) => id ? setParams({ account: id }) : setParams({})}
      // Resource detail + connect/manage are real route changes → real
      // anchors (issue #130: middle-click / Ctrl-click / "open in new tab").
      zombieHref={zombieDetailHref}
      connectHref="/connect"
      editAccountHref={editAccountHref}
      // The two hero stat tiles drill down imperatively: the waste tile wraps
      // an InfoTooltip <button>, and an <a> can't legally contain a button, so
      // these stay callbacks rather than anchors.
      onShowTrend={() => navigate('/trend')}
      onShowCosts={() => navigate(selectedAccount ? `/spend?account=${encodeURIComponent(selectedAccount)}` : '/spend')}
    />
  );
}
