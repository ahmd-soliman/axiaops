import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

// Keep ApiError/STAFF_ROLES real; stub only the network surface.
vi.mock('../api/admin.js', async (orig) => {
  const actual = await orig();
  return {
    ...actual,
    adminApi: { me: vi.fn(), login: vi.fn(), logout: vi.fn() },
  };
});

import { adminApi, ApiError } from '../api/admin.js';
import { AdminAuthProvider } from '../auth/AdminAuth.jsx';
import LoginScreen from './LoginScreen.jsx';

function renderLogin() {
  return render(
    <MemoryRouter initialEntries={['/login']}>
      <AdminAuthProvider>
        <LoginScreen />
      </AdminAuthProvider>
    </MemoryRouter>,
  );
}

describe('LoginScreen', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // No live session on mount.
    adminApi.me.mockRejectedValue(new ApiError(401, 'unauthenticated'));
  });

  it('submits credentials and calls login', async () => {
    adminApi.login.mockResolvedValue({ staff_user_id: 's1', email: 'a@x.io', roles: ['support'] });
    renderLogin();

    await userEvent.type(screen.getByLabelText('Email'), 'a@x.io');
    await userEvent.type(screen.getByLabelText('Password'), 'correct-horse-1234');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));

    await waitFor(() => expect(adminApi.login).toHaveBeenCalledWith('a@x.io', 'correct-horse-1234'));
  });

  it('shows a generic error on bad credentials (no enumeration)', async () => {
    adminApi.login.mockRejectedValue(new ApiError(401, 'invalid_credentials'));
    renderLogin();

    await userEvent.type(screen.getByLabelText('Email'), 'a@x.io');
    await userEvent.type(screen.getByLabelText('Password'), 'wrong-password-1234');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));

    expect(await screen.findByRole('alert')).toHaveTextContent(/invalid email or password/i);
  });

  it('shows a rate-limit message on 429', async () => {
    adminApi.login.mockRejectedValue(new ApiError(429, 'rate_limited'));
    renderLogin();
    await userEvent.type(screen.getByLabelText('Email'), 'a@x.io');
    await userEvent.type(screen.getByLabelText('Password'), 'whatever-secret-12');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/too many attempts/i);
  });
});
