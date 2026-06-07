// Locks in the fix for issue #130: in-app navigation must render REAL
// `<a href>` elements (not `<button onClick={navigate}>`), because only a
// real anchor gives the browser a URL to act on for middle-click /
// Ctrl-Cmd-click / "Open link in new tab". The bug was invisible to the
// previous test suite — a button "works" on plain click — so these assertions
// guard the one property that actually regressed: the presence of href.
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LinkButton, RowLink, StretchedRowLink } from './RouterLink';

function renderWithRouter(ui) {
  return render(<MemoryRouter>{ui}</MemoryRouter>);
}

describe('RouterLink primitives', () => {
  it('LinkButton renders an anchor with a resolvable href', () => {
    renderWithRouter(<LinkButton to="/trend">Trends</LinkButton>);
    const link = screen.getByRole('link', { name: 'Trends' });
    expect(link.tagName).toBe('A');
    expect(link).toHaveAttribute('href', '/trend');
  });

  it('RowLink renders an anchor with a resolvable href', () => {
    renderWithRouter(<RowLink to="/account?account=abc">Row</RowLink>);
    const link = screen.getByRole('link', { name: 'Row' });
    expect(link.tagName).toBe('A');
    expect(link).toHaveAttribute('href', '/account?account=abc');
  });

  it('StretchedRowLink exposes its destination via an aria-labelled anchor', () => {
    renderWithRouter(<StretchedRowLink to="/settings/cloud-accounts/42" label="Manage prod" />);
    const link = screen.getByRole('link', { name: 'Manage prod' });
    expect(link).toHaveAttribute('href', '/settings/cloud-accounts/42');
  });

  it('passes through click handlers and aria-current', () => {
    renderWithRouter(<LinkButton to="/" aria-current="page">Home</LinkButton>);
    expect(screen.getByRole('link', { name: 'Home' })).toHaveAttribute('aria-current', 'page');
  });
});
