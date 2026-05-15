// Pins the loading-guard posture introduced after the 2026-05-11 regression
// (MeContext.refresh() now flips loading=true on every refresh, not just the
// initial /v1/me). A naive `if (loading) return null;` made every mutation
// that called invalidate() → refresh() unmount the entire authenticated tree
// for one frame, taking local component state with it — most visibly, the
// Members page's just-set lastInvite state, which is why the post-invite
// success card (the only place the redemption URL is shown) never rendered.
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import OnboardingGate from './OnboardingGate';

// Module-level cell so each test can swap useMe()'s return value without
// re-mocking the whole module.
let meValue;
vi.mock('../context/MeContext', () => ({
  useMe: () => meValue,
}));

function renderWithRoute(path = '/settings/members') {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route element={<OnboardingGate />}>
          <Route path="*" element={<div data-testid="child">child</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe('OnboardingGate', () => {
  it('renders nothing on the initial /v1/me round-trip (loading + no me yet)', () => {
    meValue = { me: null, loading: true, error: null };
    renderWithRoute();
    expect(screen.queryByTestId('child')).not.toBeInTheDocument();
  });

  it('keeps rendering children during a background refresh once me is populated', () => {
    // The regression: a mutation's invalidate() → MeContext.refresh() flips
    // loading=true while me is still populated from the previous resolution.
    // If the gate were `if (loading) return null;` this case would unmount
    // the Outlet, destroying any local state set in the mutation's onSuccess.
    meValue = {
      me: { user_id: 'u-1', role: 'member', organization: { onboarding_completed_at: '2026-05-11T00:00:00Z' } },
      loading: true,
      error: null,
    };
    renderWithRoute();
    expect(screen.getByTestId('child')).toBeInTheDocument();
  });

  it('renders children when not loading and onboarding is complete', () => {
    meValue = {
      me: { user_id: 'u-1', role: 'member', organization: { onboarding_completed_at: '2026-05-11T00:00:00Z' } },
      loading: false,
      error: null,
    };
    renderWithRoute();
    expect(screen.getByTestId('child')).toBeInTheDocument();
  });
});
