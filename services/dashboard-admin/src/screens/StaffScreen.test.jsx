import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../api/admin.js', async (orig) => {
  const actual = await orig();
  return {
    ...actual,
    adminApi: {
      listStaff: vi.fn(),
      createStaff: vi.fn(),
      grantRole: vi.fn(),
      revokeRole: vi.fn(),
    },
  };
});

// Keep hasRole real; override useAdminAuth per test.
vi.mock('../auth/AdminAuth.jsx', async (orig) => {
  const actual = await orig();
  return { ...actual, useAdminAuth: vi.fn() };
});

import { adminApi, ApiError } from '../api/admin.js';
import { useAdminAuth } from '../auth/AdminAuth.jsx';
import StaffScreen from './StaffScreen.jsx';

function asSuperadmin() {
  useAdminAuth.mockReturnValue({ staff: { staff_user_id: 'me', email: 'boss@x.io', roles: ['superadmin'] } });
}

describe('StaffScreen', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    adminApi.listStaff.mockResolvedValue({ staff: [] });
  });

  it('blocks non-superadmins', () => {
    useAdminAuth.mockReturnValue({ staff: { roles: ['support'] } });
    render(<StaffScreen />);
    expect(screen.getByText(/only superadmins can manage staff/i)).toBeInTheDocument();
    expect(adminApi.listStaff).not.toHaveBeenCalled();
  });

  it('lists staff for a superadmin', async () => {
    asSuperadmin();
    adminApi.listStaff.mockResolvedValue({
      staff: [{ staff_user_id: 's1', email: 'ops@x.io', name: 'Ops', status: 'active', roles: ['ops'] }],
    });
    render(<StaffScreen />);
    expect(await screen.findByText('ops@x.io')).toBeInTheDocument();
  });

  it('creates a staff user', async () => {
    asSuperadmin();
    adminApi.createStaff.mockResolvedValue({ staff_user_id: 'new' });
    render(<StaffScreen />);

    await userEvent.type(screen.getByLabelText('Email'), 'new@x.io');
    await userEvent.type(screen.getByLabelText('Name'), 'New Person');
    await userEvent.type(screen.getByLabelText('Password'), 'a-strong-password-12');
    await userEvent.click(screen.getByRole('button', { name: /create staff user/i }));

    await waitFor(() =>
      expect(adminApi.createStaff).toHaveBeenCalledWith({
        email: 'new@x.io',
        name: 'New Person',
        password: 'a-strong-password-12',
        roles: ['support'],
      }),
    );
  });

  it('grants a role via the per-row select', async () => {
    asSuperadmin();
    adminApi.listStaff.mockResolvedValue({
      staff: [{ staff_user_id: 's1', email: 'sup@x.io', name: 'Sup', status: 'active', roles: ['support'] }],
    });
    adminApi.grantRole.mockResolvedValue(null);
    render(<StaffScreen />);

    const select = await screen.findByLabelText('grant role to sup@x.io');
    await userEvent.selectOptions(select, 'ops');
    await waitFor(() => expect(adminApi.grantRole).toHaveBeenCalledWith('s1', 'ops'));
  });

  it('surfaces the last-superadmin guard inline on revoke', async () => {
    asSuperadmin();
    adminApi.listStaff.mockResolvedValue({
      staff: [{ staff_user_id: 's1', email: 'solo@x.io', name: '', status: 'active', roles: ['superadmin'] }],
    });
    adminApi.revokeRole.mockRejectedValue(new ApiError(409, 'last_superadmin'));
    render(<StaffScreen />);

    const revoke = await screen.findByLabelText('revoke superadmin');
    await userEvent.click(revoke);

    expect(await screen.findByText(/cannot revoke the last superadmin/i)).toBeInTheDocument();
  });
});
