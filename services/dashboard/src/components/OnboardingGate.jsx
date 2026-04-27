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
export default function OnboardingGate() {
  const { me, loading } = useMe();
  const location = useLocation();

  if (loading) return null;

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
