import { test, expect } from '@playwright/test';
import { gotoSettled, expectNotNotFound } from './helpers';

// Access-key credential format validation on the Connect Account flow
// (`/connect`, AccessKeyTab) and the per-account edit form
// (`/settings/cloud-accounts/:id`, AccountSettingsScreen). Regression
// coverage for the validation added in account_role.go/handler.go +
// ConnectScreen.jsx/AccountSettingsScreen.jsx (issue #74) — previously any
// string was accepted as access_key_id/secret_key/region, silently
// AES-encrypted, and stored, with the mismatch only surfacing opaquely on
// the account's first scan attempt.
//
// AWS_ACCESS_KEY_ID_RE / AWS_SECRET_KEY_RE / AWS_REGION_RE (ConnectScreen.jsx)
// are shape checks against AWS's published formats, not exhaustive
// allowlists — a region like "xx-fake-1" passes the region check even
// though "xx" isn't a real AWS region code. That's confirmed intentional
// behaviour (see the dedicated test below), not a bug to fix here.

const AWS_EXAMPLE_ACCESS_KEY_ID = 'AKIAIOSFODNN7EXAMPLE';
const AWS_EXAMPLE_SECRET_KEY = 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY';

async function fillAccessKeyFields(
  page: import('@playwright/test').Page,
  fields: { accessKeyId?: string; secretKey?: string; region?: string },
): Promise<void> {
  if (fields.accessKeyId !== undefined) {
    await page.getByPlaceholder('AKIAIOSFODNN7EXAMPLE').first().fill(fields.accessKeyId);
  }
  if (fields.secretKey !== undefined) {
    await page.locator('input[placeholder*="wJalrXUtnFEMI"]').first().fill(fields.secretKey);
  }
  if (fields.region !== undefined) {
    await page.locator('input[placeholder="eu-central-1"]').first().fill(fields.region);
  }
}

test.describe('connect account — access-key credential validation', () => {
  test.beforeEach(async ({ page }) => {
    await gotoSettled(page, '/connect');
    const accessKeysTab = page.getByRole('button', { name: /Access Keys/i });
    if (await accessKeysTab.count()) {
      await accessKeysTab.first().click();
    }
  });

  test('rejects a malformed access key ID', async ({ page }) => {
    await fillAccessKeyFields(page, {
      accessKeyId: 'someone@example.com',
      secretKey: AWS_EXAMPLE_SECRET_KEY,
      region: 'eu-central-1',
    });
    await page.getByRole('button', { name: 'Generate connection' }).click();
    await expect(page.getByText('Access Key ID must look like an AWS key')).toBeVisible();
  });

  test('rejects a too-short secret key', async ({ page }) => {
    await fillAccessKeyFields(page, {
      accessKeyId: AWS_EXAMPLE_ACCESS_KEY_ID,
      secretKey: 'tooshort',
      region: 'eu-central-1',
    });
    await page.getByRole('button', { name: 'Generate connection' }).click();
    await expect(page.getByText("Secret Access Key must be 40 characters")).toBeVisible();
  });

  test("rejects a 40-character secret key outside AWS's key alphabet", async ({ page }) => {
    // Right length, wrong charset (a space isn't in [A-Za-z0-9/+]) — distinct
    // from the too-short case above, since a length-only check would pass this.
    const wrongCharsetSecret = 'wJalrXUtnFEMI K7MDENGbPxRfiCYEXAMPLEKEYA';
    expect(wrongCharsetSecret).toHaveLength(40);
    await fillAccessKeyFields(page, {
      accessKeyId: AWS_EXAMPLE_ACCESS_KEY_ID,
      secretKey: wrongCharsetSecret,
      region: 'eu-central-1',
    });
    await page.getByRole('button', { name: 'Generate connection' }).click();
    await expect(page.getByText("Secret Access Key must be 40 characters")).toBeVisible();
  });

  test('rejects a malformed region', async ({ page }) => {
    await fillAccessKeyFields(page, {
      accessKeyId: AWS_EXAMPLE_ACCESS_KEY_ID,
      secretKey: AWS_EXAMPLE_SECRET_KEY,
      region: 'not-a-region',
    });
    await page.getByRole('button', { name: 'Generate connection' }).click();
    await expect(page.getByText('Region must be a valid AWS region')).toBeVisible();
  });

  test('accepts a shape-valid but non-existent region (known limitation, not a bug)', async ({ page }) => {
    await fillAccessKeyFields(page, {
      accessKeyId: AWS_EXAMPLE_ACCESS_KEY_ID,
      secretKey: AWS_EXAMPLE_SECRET_KEY,
      region: 'xx-fake-1',
    });
    await page.getByRole('button', { name: 'Generate connection' }).click();
    await expect(page.getByText('Region must be a valid AWS region')).not.toBeVisible();
  });

  test('accepts well-formed AWS example credentials', async ({ page }) => {
    await fillAccessKeyFields(page, {
      accessKeyId: AWS_EXAMPLE_ACCESS_KEY_ID,
      secretKey: AWS_EXAMPLE_SECRET_KEY,
      region: 'eu-central-1',
    });
    await page.getByRole('button', { name: 'Generate connection' }).click();
    // No client-side validation error, and the flow advances past the
    // credential form to the CUR CloudFormation step.
    await expect(page.getByText('Access Key ID must look like an AWS key')).not.toBeVisible();
    await expect(page.getByText('Your access keys are saved')).toBeVisible();
  });
});

test.describe('cloud account edit — access-key credential validation', () => {
  // seed-account-001 ("Seed Production AWS") from scripts/seed_test_data.sh.
  // Only ever exercises the rejection path here — validateCredentialFields
  // returns before updateAccount() is called, so the seeded account is never
  // actually mutated. Confirmed via the account's own access_key_id
  // afterward, not just the inline error message.
  const SEED_ACCOUNT_ID = 'seed-account-001';

  test('rejects a malformed access key ID without saving', async ({ page }) => {
    const before = await (await page.request.get(`/api/v1/accounts/${SEED_ACCOUNT_ID}`)).json();

    await gotoSettled(page, `/settings/cloud-accounts/${SEED_ACCOUNT_ID}`);
    await expectNotNotFound(page);
    await page.getByPlaceholder('AKIAIOSFODNN7EXAMPLE').first().fill('someone@example.com');
    await page.getByRole('button', { name: 'Save Changes' }).click();
    await expect(page.getByText('Access Key ID must look like an AWS key')).toBeVisible();

    const after = await (await page.request.get(`/api/v1/accounts/${SEED_ACCOUNT_ID}`)).json();
    expect(after.access_key_id).toBe(before.access_key_id);
  });
});
