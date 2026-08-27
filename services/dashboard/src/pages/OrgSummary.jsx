import { useSearchParams, Navigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchAccounts } from '../api/client';
import OrgSummaryScreen from '../screens/OrgSummaryScreen';
import WhatsNextPanel from '../components/onboarding/WhatsNextPanel';
import { zombieDetailHref } from '../utils/links';

// Wrapper for `/` — the read-only organization summary. Mirrors Overview.jsx's
// account fetch + zero-accounts onboarding redirect, and adds a single-account
// redirect: the dominant self-hosted operator (one connected account) keeps
// landing on the actionable workbench at /account, so the org summary only ever
// renders for orgs with 2+ accounts.
export default function OrgSummary() {
  const [params] = useSearchParams();
  const accounts = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });

  // Wait for the account list before deciding: until it resolves we can't tell
  // zero/one/many apart, and rendering the summary early would flash it (then
  // redirect) for the common single-account operator and fire two org-wide
  // queries needlessly. Mirrors Settings.jsx's load gate.
  if (accounts.isPending) return null;

  // If the account list itself fails, bail with a plain message rather than
  // falling through to render the summary as "0 connected accounts" and firing
  // the org-wide queries against a state we can't trust.
  if (accounts.isError) {
    return (
      <div style={{ maxWidth: 560, margin: '64px auto', padding: '0 20px', textAlign: 'center' }}>
        <h2 style={{ fontSize: 18, fontWeight: 800, color: 'var(--color-text)', margin: '0 0 8px' }}>
          Couldn’t load your accounts
        </h2>
        <p style={{ fontSize: 14, color: 'var(--color-text-muted)', lineHeight: 1.5, margin: 0 }}>
          Refresh the page to try again.
        </p>
      </div>
    );
  }

  // Preserve the onboarding redirect from the old `/`: zero accounts → connect.
  // skip_connect=1 lets a user reach the (sparse) summary even with no accounts.
  const skipConnect = params.get('skip_connect') === '1';
  if (accounts.data?.length === 0 && !skipConnect) return <Navigate to="/connect?onboarding=1" replace />;

  // Single account → the workbench, scoped to that account. accounts[0].id is the
  // internal UUID the workbench's ?account= param expects.
  if (accounts.data?.length === 1) {
    return <Navigate to={`/zombies?account=${encodeURIComponent(accounts.data[0].id)}`} replace />;
  }

  return (
    <>
      <WhatsNextPanel />
      <OrgSummaryScreen
        accounts={accounts.data ?? []}
        viewAccountsHref="/zombies"
        accountHref={(id) => `/zombies?account=${encodeURIComponent(id)}`}
        zombieHref={zombieDetailHref}
        serviceHref={(svc) => `/zombies?service=${encodeURIComponent(svc)}`}
        auditHref="/settings/audit"
        trendsHref="/trend"
      />
    </>
  );
}
