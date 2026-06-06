import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../api/admin.js', async (orig) => {
  const actual = await orig();
  return { ...actual, adminApi: { listTenants: vi.fn() } };
});

import { adminApi } from '../api/admin.js';
import TenantsScreen from './TenantsScreen.jsx';

describe('TenantsScreen', () => {
  beforeEach(() => vi.clearAllMocks());

  it('renders a row per tenant', async () => {
    adminApi.listTenants.mockResolvedValue({
      tenants: [
        { organization_id: 'org-1', org_code: 'acme', name: 'Acme', created_at: '2026-06-01T00:00:00Z' },
        { organization_id: 'org-2', org_code: 'globex', name: 'Globex', created_at: '2026-06-02T00:00:00Z' },
      ],
    });
    render(
      <MemoryRouter>
        <TenantsScreen />
      </MemoryRouter>,
    );
    expect(await screen.findByText('Acme')).toBeInTheDocument();
    expect(screen.getByText('Globex')).toBeInTheDocument();
    expect(screen.getByText('acme')).toBeInTheDocument();
  });

  it('shows an empty state when there are no tenants', async () => {
    adminApi.listTenants.mockResolvedValue({ tenants: [] });
    render(
      <MemoryRouter>
        <TenantsScreen />
      </MemoryRouter>,
    );
    expect(await screen.findByText(/no organizations yet/i)).toBeInTheDocument();
  });

  it('surfaces a load error', async () => {
    adminApi.listTenants.mockRejectedValue(new Error('boom'));
    render(
      <MemoryRouter>
        <TenantsScreen />
      </MemoryRouter>,
    );
    expect(await screen.findByText('boom')).toBeInTheDocument();
  });
});
