package kinde

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// InviteUser POSTs to Kinde's organization-users endpoint with send_invite=true,
// which both creates the user (if needed) and dispatches the invitation email.
//
// API shape (from Kinde docs at the time of writing — subject to drift):
//
//	POST /api/v1/organizations/{org_code}/users
//	{ "users": [{ "email": "...", "first_name": "...", "send_invite": true }] }
//
//	200 { "users_added": [{ "user_id": "...", "invitation_id": "..." }] }
//
// On Kinde error, returns *kindeError so handlers can distinguish 4xx (client
// fix) from 5xx (rollback + 502).
func (c *HTTPClient) InviteUser(ctx context.Context, orgCode, email, fullName string) (string, string, error) {
	if orgCode == "" || email == "" {
		return "", "", fmt.Errorf("kinde: invite user: orgCode and email required")
	}
	type kindeUser struct {
		Email      string `json:"email"`
		FirstName  string `json:"first_name,omitempty"`
		SendInvite bool   `json:"send_invite"`
	}
	type reqBody struct {
		Users []kindeUser `json:"users"`
	}
	body, err := json.Marshal(reqBody{Users: []kindeUser{{Email: email, FirstName: fullName, SendInvite: true}}})
	if err != nil {
		return "", "", fmt.Errorf("kinde: invite user encode: %w", err)
	}

	type kindeAdded struct {
		UserID       string `json:"user_id"`
		InvitationID string `json:"invitation_id"`
	}
	var resp struct {
		UsersAdded []kindeAdded `json:"users_added"`
	}
	endpoint := fmt.Sprintf("%s/api/v1/organizations/%s/users", c.mgmtAPIURL, url.PathEscape(orgCode))
	if err := c.do(ctx, "invite_user", "POST", endpoint, body, &resp); err != nil {
		return "", "", err
	}
	if len(resp.UsersAdded) == 0 {
		return "", "", fmt.Errorf("kinde: invite user: empty users_added in response")
	}
	added := resp.UsersAdded[0]
	return added.InvitationID, added.UserID, nil
}

// RemoveUser deletes a user from a Kinde organization.
//
// API shape:
//
//	DELETE /api/v1/organizations/{org_code}/users/{user_id}
//
// 404 is treated as success (idempotent — Kinde already lost the link).
func (c *HTTPClient) RemoveUser(ctx context.Context, orgCode, kindeUserID string) error {
	if orgCode == "" || kindeUserID == "" {
		// Treat missing IDs as a no-op success — handler may call RemoveUser
		// before Kinde IDs were ever recorded (e.g. invite failed before we
		// could persist them). The local row should still flip to revoked.
		return nil
	}
	endpoint := fmt.Sprintf(
		"%s/api/v1/organizations/%s/users/%s",
		c.mgmtAPIURL, url.PathEscape(orgCode), url.PathEscape(kindeUserID),
	)
	err := c.do(ctx, "remove_user", "DELETE", endpoint, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// RenameOrganization updates the organization's display name in Kinde.
//
// API shape:
//
//	PATCH /api/v1/organization?code={org_code}
//	{ "name": "..." }
//
// Used by PATCH /v1/organizations/me. The handler local-commits inside a txn
// only after this returns 2xx — see docs/onboarding-wizard.md §5.1.
func (c *HTTPClient) RenameOrganization(ctx context.Context, orgCode, name string) error {
	if orgCode == "" {
		return fmt.Errorf("kinde: rename organization: orgCode required")
	}
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return fmt.Errorf("kinde: rename organization encode: %w", err)
	}
	endpoint := fmt.Sprintf("%s/api/v1/organization?code=%s", c.mgmtAPIURL, url.QueryEscape(orgCode))
	return c.do(ctx, "rename_organization", "PATCH", endpoint, body, nil)
}
