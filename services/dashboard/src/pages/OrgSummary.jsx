import { useNavigate, useSearchParams, Navigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchAccounts } from '../api/client';
import OrgSummaryScreen from '../screens/OrgSummaryScreen';
import WhatsNextPanel from '../components/onboarding/WhatsNextPanel';

// Wrapper for `/` — the read-only organization summary. Mirrors Overview.jsx's
// account fetch + zero-accounts onboarding redirect, and adds a single-account
// redirect: the dominant self-hosted operator (one connected account) keeps
// landing on the actionable workbench at /account, so the org summary only ever
// renders for orgs with 2+ accounts.
export default function OrgSummary() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const accounts = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });

  // Preserve the onboarding redirect from the old `/`: zero accounts → connect.
  // skip_connect=1 lets a user reach the (sparse) summary even with no accounts.
  const skipConnect = params.get('skip_connect') === '1';
  if (accounts.data?.length === 0 && !skipConnect) return <Navigate to="/connect?onboarding=1" replace />;

  // Single account → the workbench, scoped to that account. accounts[0].id is the
  // internal UUID the workbench's ?account= param expects.
  if (accounts.data?.length === 1) {
    return <Navigate to={`/account?account=${encodeURIComponent(accounts.data[0].id)}`} replace />;
  }

  return (
    <>
      <WhatsNextPanel />
      <OrgSummaryScreen
        accounts={accounts.data ?? []}
        onViewAccounts={() => navigate('/account')}
      />
    </>
  );
}
