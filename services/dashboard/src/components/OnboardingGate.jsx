import { Outlet, Navigate, useLocation } from 'react-router-dom';
import { useMe } from '../context/MeContext';

// OnboardingGate steers fresh-org owners through /onboarding before the main
// dashboard, and prevents already-completed orgs from landing on the wizard.
// See docs/onboarding-wizard.md §8.2.
//
// Decision matrix (only after MeContext finishes loading):
//   * role === 'owner' && !onboarding_completed_at && path !== /onboarding/* → /onboarding
//   * onboarding_completed_at && path startsWith /onboarding                → /
//   * else                                                                  → render Outlet
//
// Invited users (role member/viewer/admin) bypass naturally on the first
// condition. Renders nothing while MeContext loads to avoid a flash.
//
// The loading guard is intentionally gated on `!me` (matches AuthGuard's
// pattern): suppress only on the initial /v1/me round-trip, not on
// subsequent refresh() calls from MeContext. MeContext.refresh() flips
// loading=true at the start of every refresh — including the refresh
// that mutations fire via invalidate() — so a broader `if (loading)`
// would unmount the entire authenticated tree on every mutation, taking
// local component state with it (e.g. the just-set lastInvite state on
// Settings → Members, which makes the post-invite success card never
// render). Once `me` is populated, keep rendering the existing decision
// during background refreshes.
//
// Unlike AuthGuard, this gate doesn't check `!error`. That's fine
// today because OnboardingGate is always rendered *inside* AuthGuard
// in the route tree — by the time a request reaches here, AuthGuard
// has already handled the error case (rendered ErrorPage or redirected
// to /login). If the route tree is ever restructured so this gate
// wraps AuthGuard, add `!error` to the guard.
export default function OnboardingGate() {
  const { me, loading } = useMe();
  const location = useLocation();

  if (loading && !me) return null;

  const role = me?.role || '';
  const completed = !!me?.organization?.onboarding_completed_at;
  const onWizard = location.pathname.startsWith('/onboarding');

  if (role === 'owner' && !completed && !onWizard) {
    return <Navigate to="/onboarding" replace />;
  }
  if (completed && onWizard) {
    return <Navigate to="/" replace />;
  }
  return <Outlet />;
}
