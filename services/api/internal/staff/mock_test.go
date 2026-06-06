package staff_test

import (
	"context"
	"strings"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// mockStore is an in-memory staff.Store for black-box handler tests. It is
// deliberately simple — enough to exercise the handler branches without a DB.
type mockStore struct {
	users     map[string]model.StaffUser           // id → user
	grants    map[string][]model.StaffRoleGrant    // id → grants
	orgs      []model.Organization
	summaries map[string]model.StaffTenantSummary  // org id → summary

	createErr error // forced error from CreateStaffUser
}

func newMockStore() *mockStore {
	return &mockStore{
		users:     map[string]model.StaffUser{},
		grants:    map[string][]model.StaffRoleGrant{},
		summaries: map[string]model.StaffTenantSummary{},
	}
}

// addStaff registers a staff user with a known hash + roles.
func (m *mockStore) addStaff(id, email, name, passwordHash, status string, roles ...model.StaffRole) {
	m.users[id] = model.StaffUser{ID: id, Email: email, Name: name, PasswordHash: passwordHash, Status: status}
	g := make([]model.StaffRoleGrant, 0, len(roles))
	for _, r := range roles {
		g = append(g, model.StaffRoleGrant{StaffUserID: id, Role: r})
	}
	m.grants[id] = g
}

func (m *mockStore) CreateStaffUser(_ context.Context, in storage.CreateStaffUserInput) (model.StaffUser, error) {
	if m.createErr != nil {
		return model.StaffUser{}, m.createErr
	}
	for _, u := range m.users {
		if strings.EqualFold(u.Email, in.Email) {
			return model.StaffUser{}, storage.ErrStaffEmailExists
		}
	}
	id := in.ID
	if id == "" {
		id = "staff-" + in.Email
	}
	u := model.StaffUser{ID: id, Email: in.Email, Name: in.Name, PasswordHash: in.PasswordHash, Status: "active"}
	m.users[id] = u
	m.addRoles(id, in.Roles)
	return u, nil
}

func (m *mockStore) addRoles(id string, roles []model.StaffRole) {
	for _, r := range roles {
		m.grants[id] = append(m.grants[id], model.StaffRoleGrant{StaffUserID: id, Role: r})
	}
}

func (m *mockStore) LookupStaffUserByEmail(_ context.Context, email string) (model.StaffUser, []model.StaffRoleGrant, error) {
	for _, u := range m.users {
		if strings.EqualFold(u.Email, email) {
			return u, m.grants[u.ID], nil
		}
	}
	return model.StaffUser{}, nil, storage.ErrStaffNotFound
}

func (m *mockStore) GetStaffUserByID(_ context.Context, id string) (model.StaffUser, []model.StaffRoleGrant, error) {
	u, ok := m.users[id]
	if !ok {
		return model.StaffUser{}, nil, storage.ErrStaffNotFound
	}
	return u, m.grants[id], nil
}

func (m *mockStore) ListStaffUsers(_ context.Context) ([]model.StaffUser, [][]model.StaffRoleGrant, error) {
	users := make([]model.StaffUser, 0, len(m.users))
	grants := make([][]model.StaffRoleGrant, 0, len(m.users))
	for id, u := range m.users {
		users = append(users, u)
		grants = append(grants, m.grants[id])
	}
	return users, grants, nil
}

func (m *mockStore) GrantStaffRole(_ context.Context, staffUserID string, role model.StaffRole, _ string) error {
	if _, ok := m.users[staffUserID]; !ok {
		return storage.ErrStaffNotFound
	}
	for _, g := range m.grants[staffUserID] {
		if g.Role == role {
			return nil
		}
	}
	m.grants[staffUserID] = append(m.grants[staffUserID], model.StaffRoleGrant{StaffUserID: staffUserID, Role: role})
	return nil
}

func (m *mockStore) RevokeStaffRole(ctx context.Context, staffUserID string, role model.StaffRole) error {
	// Replicate the store's atomic last-superadmin guard so handler tests
	// exercise the real branch.
	if role == model.StaffRoleSuperadmin {
		targetHolds := false
		for _, g := range m.grants[staffUserID] {
			if g.Role == model.StaffRoleSuperadmin {
				targetHolds = true
			}
		}
		if targetHolds {
			total, _ := m.CountStaffWithRole(ctx, model.StaffRoleSuperadmin)
			if total <= 1 {
				return storage.ErrLastStaffSuperadmin
			}
		}
	}
	out := m.grants[staffUserID][:0]
	for _, g := range m.grants[staffUserID] {
		if g.Role != role {
			out = append(out, g)
		}
	}
	m.grants[staffUserID] = out
	return nil
}

func (m *mockStore) CountStaffWithRole(_ context.Context, role model.StaffRole) (int, error) {
	n := 0
	for _, gs := range m.grants {
		for _, g := range gs {
			if g.Role == role {
				n++
			}
		}
	}
	return n, nil
}

func (m *mockStore) ListAllOrganizations(_ context.Context) ([]model.Organization, error) {
	return m.orgs, nil
}

func (m *mockStore) StaffTenantSummary(_ context.Context, organizationID string) (model.StaffTenantSummary, error) {
	s, ok := m.summaries[organizationID]
	if !ok {
		return model.StaffTenantSummary{}, storage.ErrOrganizationNotFound
	}
	return s, nil
}
