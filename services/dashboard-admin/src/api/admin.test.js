import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { adminApi, ApiError } from './admin.js';

describe('adminApi client', () => {
  beforeEach(() => {
    global.fetch = vi.fn();
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('sends credentials and parses JSON on success', async () => {
    global.fetch.mockResolvedValue(
      new Response(JSON.stringify({ tenants: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    );
    const out = await adminApi.listTenants();
    expect(out).toEqual({ tenants: [] });
    const [, opts] = global.fetch.mock.calls[0];
    expect(opts.credentials).toBe('include');
  });

  it('returns null for 204', async () => {
    global.fetch.mockResolvedValue(new Response(null, { status: 204 }));
    expect(await adminApi.logout()).toBeNull();
  });

  it('throws ApiError carrying status + backend code on failure', async () => {
    global.fetch.mockResolvedValue(
      new Response(JSON.stringify({ error: 'last_superadmin', message: 'cannot revoke the last superadmin' }), {
        status: 409,
      }),
    );
    await expect(adminApi.revokeRole('s1', 'superadmin')).rejects.toMatchObject({
      status: 409,
      code: 'last_superadmin',
    });
  });

  it('wraps network failures as ApiError(network_error)', async () => {
    global.fetch.mockRejectedValue(new TypeError('Failed to fetch'));
    const err = await adminApi.me().catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 0, code: 'network_error' });
  });
});
