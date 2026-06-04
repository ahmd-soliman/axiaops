package staff

import "axiaops.io/shared/model"

// grantsToRoles projects role grants to a role slice.
func grantsToRoles(grants []model.StaffRoleGrant) []model.StaffRole {
	roles := make([]model.StaffRole, 0, len(grants))
	for _, g := range grants {
		roles = append(roles, g.Role)
	}
	return roles
}

// roleStrings renders roles as a JSON-friendly []string (never nil — an empty
// roles set serialises as [] so the UI doesn't special-case null).
func roleStrings(roles []model.StaffRole) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, string(r))
	}
	return out
}
