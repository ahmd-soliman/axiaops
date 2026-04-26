// Permission string constants — mirrors services/shared/authz/roles.go.
// Backend is the source of truth; this file exists so the dashboard never
// hard-codes a permission string and a typo can be caught at build time
// (`PERM.ORGANIZATIN_DELETE` is a ReferenceError; `'organizatin:delete'` is silent).
//
// Pass these to MeContext's `can(perm)` helper. Keep in sync when the
// backend adds or renames a permission.
export const PERM = {
  ACCOUNTS_READ:         'accounts:read',
  ACCOUNTS_WRITE:        'accounts:write',
  ACCOUNTS_DELETE:       'accounts:delete',
  ACCOUNTS_SCAN:         'accounts:scan',

  ZOMBIES_READ:          'zombies:read',
  ZOMBIES_DISMISS:       'zombies:dismiss',

  SNAPSHOTS_READ:        'snapshots:read',
  COSTS_READ:            'costs:read',
  RESOURCES_READ:        'resources:read',
  AUDIT_READ:            'audit:read',

  MEMBERS_READ:          'members:read',
  MEMBERS_INVITE:        'members:invite',
  MEMBERS_MANAGE_BASIC:  'members:manage_basic',
  MEMBERS_MANAGE_ADMIN:  'members:manage_admin',

  ORGANIZATION_TRANSFER: 'organization:transfer',
  ORGANIZATION_DELETE:   'organization:delete',
  DATA_EXPORT:           'data:export',
};
