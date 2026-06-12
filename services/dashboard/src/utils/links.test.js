import { describe, it, expect } from 'vitest';
import { zombieDetailHref, editAccountHref } from './links';

describe('zombieDetailHref', () => {
  it('builds the resource detail deep-link with encoded query params', () => {
    const href = zombieDetailHref({
      resource_id: 'i-0abc123',
      internal_account_id: 'acct-1',
      region: 'eu-central-1',
      service: 'AmazonEC2',
    });
    expect(href).toBe('/detail/i-0abc123?account=acct-1&region=eu-central-1&service=AmazonEC2');
  });

  it('percent-encodes a resource id containing slashes/spaces in the path', () => {
    const href = zombieDetailHref({
      resource_id: 'arn:aws:s3:::my bucket',
      internal_account_id: 'acct-1',
      region: 'us-east-1',
      service: 'AmazonS3',
    });
    expect(href.startsWith('/detail/arn%3Aaws%3As3%3A%3A%3Amy%20bucket?')).toBe(true);
  });

  it('tolerates missing optional fields without emitting "undefined"', () => {
    const href = zombieDetailHref({ resource_id: 'r1' });
    expect(href).toBe('/detail/r1?account=&region=&service=');
  });
});

describe('editAccountHref', () => {
  it('links to the canonical settings path (no redirect bounce)', () => {
    expect(editAccountHref({ id: '42' })).toBe('/settings/cloud-accounts/42');
  });
});
