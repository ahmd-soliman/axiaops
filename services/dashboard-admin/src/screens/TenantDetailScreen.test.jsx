import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';

vi.mock('../api/admin.js', async (orig) => {
  const actual = await orig();
  return { ...actual, adminApi: { getTenant: vi.fn() } };
});

import { adminApi, ApiError } from '../api/admin.js';
import TenantDetailScreen from './TenantDetailScreen.jsx';

function renderAt(id) {
  return render(
    <MemoryRouter initialEntries={[`/tenants/${id}`]}>
      <Routes>
        <Route path="/tenants/:id" element={<TenantDetailScreen />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('TenantDetailScreen', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows the summary and no FinOps detail', async () => {
    adminApi.getTenant.mockResolvedValue({
      organization_id: 'org-1',
      org_code: 'acme',
      name: 'Acme',
      created_at: '2026-06-01T00:00:00Z',
      onboarded_at: null,
      account_count: 3,
      last_scan_at: null,
      latest_total_zombies: 7,
      latest_potential_savings: 0,
    });
    renderAt('org-1');

    expect(await screen.findByRole('heading', { name: 'Acme' })).toBeInTheDocument();
    expect(screen.getByText('3')).toBeInTheDocument(); // account count
    // The break-glass boundary copy is present (no zombie/cost rows rendered).
    expect(screen.getByText(/requires an audited break-glass grant/i)).toBeInTheDocument();
  });

  it('renders a not-found message on 404', async () => {
    adminApi.getTenant.mockRejectedValue(new ApiError(404, 'not_found', 'no such tenant'));
    renderAt('ghost');
    expect(await screen.findByText(/no such tenant/i)).toBeInTheDocument();
  });
});
