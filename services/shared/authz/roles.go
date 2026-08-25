// Package authz defines AxiaOps roles and permissions for RBAC enforcement.
//
// The model is intentionally coarse: four hardcoded roles in a strict hierarchy
// (owner > admin > member > viewer), each granting a fixed set of permissions.
// Permissions are checked at the HTTP middleware layer via Allows. The role
// itself is stored per-(user, organization) in the memberships table — see
// docs/AUTHENTICATION.md (§2) for full rationale.
package authz

// Role names a tier in the RBAC hierarchy.
type Role string

// Permission names a discrete capability that handlers can require.
type Permission string

// Role constants. The string values are persisted in the memberships.role
// column and validated by the CHECK constraint in migration 015.
const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleViewer Role = "viewer"
)

// Permission constants. The naming convention is `<resource>:<verb>`.
// Add new permissions here when adding new endpoints.
const (
	PermAccountsRead   Permission = "accounts:read"
	PermAccountsWrite  Permission = "accounts:write"
	PermAccountsDelete Permission = "accounts:delete"
	PermAccountsScan   Permission = "accounts:scan"

	PermZombiesRead    Permission = "zombies:read"
	PermZombiesDismiss Permission = "zombies:dismiss"

	PermSnapshotsRead Permission = "snapshots:read"
	PermCostsRead     Permission = "costs:read"
	PermResourcesRead Permission = "resources:read"
	PermAuditRead     Permission = "audit:read"

	PermMembersRead        Permission = "members:read"
	PermMembersInvite      Permission = "members:invite"
	PermMembersManageBasic Permission = "members:manage_basic"
	PermMembersManageAdmin Permission = "members:manage_admin"

	PermOrganizationTransfer Permission = "organization:transfer"
	PermOrganizationDelete   Permission = "organization:delete"
	PermOrganizationUpdate   Permission = "organization:update"
	PermDataExport           Permission = "data:export"

	// Phase B2 — Native OIDC RP. SSO config is owner-only because
	// misconfiguration locks the org out of its own data; the read tier
	// (sso:read) is viewer+ so the dashboard can render the SSO settings
	// pane in read-only mode for non-owners.
	PermSSORead         Permission = "sso:read"
	PermSSOManage       Permission = "sso:manage"
	PermSSODomainVerify Permission = "sso:domain_verify"

	// Notification channels. Read is viewer+ so the
	// dashboard can render the Integrations pane; manage is admin+ because a
	// channel carries credentials (SMTP creds / webhook tokens) and triggers
	// outbound mail/Slack — the same tier as accounts:delete.
	PermChannelsRead   Permission = "channels:read"
	PermChannelsManage Permission = "channels:manage"
)

// rolePermissions maps each role to its complete permission set, including
// permissions inherited from lower roles. Built once at init().
var rolePermissions = map[Role]map[Permission]bool{}

// roleOrder tracks the inheritance chain from lowest to highest privilege.
// Each role inherits from all roles to its left.
var roleOrder = []Role{RoleViewer, RoleMember, RoleAdmin, RoleOwner}

// directGrants is what each role contributes on top of the role below it.
var directGrants = map[Role][]Permission{
	RoleViewer: {
		PermAccountsRead,
		PermZombiesRead,
		PermSnapshotsRead,
		PermCostsRead,
		PermResourcesRead,
		PermAuditRead,
		PermMembersRead,
		PermSSORead,
		PermChannelsRead,
	},
	RoleMember: {
		PermAccountsWrite,
		PermAccountsScan,
		PermZombiesDismiss,
	},
	RoleAdmin: {
		PermAccountsDelete,
		PermMembersInvite,
		PermMembersManageBasic,
		PermChannelsManage,
	},
	RoleOwner: {
		PermMembersManageAdmin,
		PermOrganizationTransfer,
		PermOrganizationDelete,
		PermOrganizationUpdate,
		PermDataExport,
		PermSSOManage,
		PermSSODomainVerify,
	},
}

func init() {
	for _, r := range roleOrder {
		set := map[Permission]bool{}
		// Inherit everything from the role one tier below this one.
		idx := indexOf(roleOrder, r)
		for i := 0; i < idx; i++ {
			for p := range rolePermissions[roleOrder[i]] {
				set[p] = true
			}
		}
		// Add direct grants for this tier.
		for _, p := range directGrants[r] {
			set[p] = true
		}
		rolePermissions[r] = set
	}
}

func indexOf(roles []Role, target Role) int {
	for i, r := range roles {
		if r == target {
			return i
		}
	}
	return -1
}

// Allows reports whether the given role grants the permission. Returns false
// for unknown roles and for the zero value — fail-closed semantics so a missing
// membership row never silently grants access.
func Allows(role Role, perm Permission) bool {
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	return perms[perm]
}

// PermissionsOf returns a deterministic slice of permissions granted to the
// role. Used by GET /v1/me so the dashboard can hide controls without a
// round-trip per check. Returns nil for unknown roles.
func PermissionsOf(role Role) []Permission {
	perms, ok := rolePermissions[role]
	if !ok {
		return nil
	}
	out := make([]Permission, 0, len(perms))
	for p := range perms {
		out = append(out, p)
	}
	// Stable order keeps responses cacheable and tests deterministic.
	sortPermissions(out)
	return out
}

func sortPermissions(perms []Permission) {
	// Tiny insertion sort — set is small (under 20 entries) and we want to
	// avoid pulling in sort just for this.
	for i := 1; i < len(perms); i++ {
		for j := i; j > 0 && perms[j-1] > perms[j]; j-- {
			perms[j-1], perms[j] = perms[j], perms[j-1]
		}
	}
}
