# User Onboarding & Organization Creation

This document outlines the complete flow for new user registration, authentication, and organization setup in AxiaOps.

## Overview

AxiaOps uses Kinde for authentication with custom organization management. New users go through a streamlined onboarding process that creates both their user account and organization in a single flow.

## Step-by-Step User Flow

### 1. Initial Registration/Login

**User Journey:**
```
User visits AxiaOps → Click "Sign Up" → Redirected to Kinde → Complete registration → Return to AxiaOps
```

**What Kinde handles:**
- Email/password registration OR social login (Google, GitHub, etc.)
- Email verification
- Returns to AxiaOps with authorization code

### 2. Authentication Callback Processing

```go
// After Kinde callback, check if user exists in AxiaOps
func handleKindeCallback(w http.ResponseWriter, r *http.Request) {
    token := exchangeCodeForToken(r.URL.Query().Get("code"))
    claims := validateToken(token)
    
    user := getUserByKindeID(claims.Sub)
    if user == nil {
        // New user - redirect to onboarding
        http.Redirect(w, r, "/onboarding", 302)
        return
    }
    
    // Existing user - redirect to dashboard
    http.Redirect(w, r, "/dashboard", 302)
}
```

### 3. Organization Creation (Onboarding)

**Frontend Onboarding Form:**
```jsx
const OnboardingForm = () => {
  const [orgName, setOrgName] = useState('');
  const [loading, setLoading] = useState(false);
  
  const handleSubmit = async (e) => {
    e.preventDefault();
    setLoading(true);
    
    try {
      await fetch('/api/v1/organizations', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${token}`
        },
        body: JSON.stringify({ org_name: orgName })
      });
      
      // Redirect to dashboard
      window.location.href = '/dashboard';
    } catch (error) {
      console.error('Failed to create organization:', error);
    } finally {
      setLoading(false);
    }
  };
  
  return (
    <form onSubmit={handleSubmit}>
      <h2>Welcome to AxiaOps</h2>
      <p>Let's set up your organization to get started.</p>
      
      <input 
        type="text"
        placeholder="Organization name (e.g., Acme Corp)"
        value={orgName}
        onChange={(e) => setOrgName(e.target.value)}
        required
        minLength={2}
        maxLength={100}
      />
      
      <button type="submit" disabled={loading}>
        {loading ? 'Creating...' : 'Create Organization'}
      </button>
    </form>
  );
};
```

### 4. Backend Organization Creation

```go
func createOrganization(w http.ResponseWriter, r *http.Request) {
    claims := getClaimsFromToken(r)
    
    // Check if user already has an organization
    existingUser := getUserByKindeID(claims.Sub)
    if existingUser != nil {
        http.Error(w, "User already has an organization", http.StatusConflict)
        return
    }
    
    var req struct {
        OrgName string `json:"org_name"`
    }
    
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "Invalid request body", http.StatusBadRequest)
        return
    }
    
    // Validate organization name
    if len(req.OrgName) < 2 || len(req.OrgName) > 100 {
        http.Error(w, "Organization name must be 2-100 characters", http.StatusBadRequest)
        return
    }
    
    tx := db.Begin()
    defer tx.Rollback()
    
    // Create organization record
    org := &Organization{
        ID:       generateUUID(),
        Name:     strings.TrimSpace(req.OrgName),
        OwnerID:  claims.Sub,
        KindeOrg: claims.Org, // Kinde org ID for RLS
        CreatedAt: time.Now(),
    }
    
    if err := tx.Create(org).Error; err != nil {
        http.Error(w, "Failed to create organization", http.StatusInternalServerError)
        return
    }
    
    // Create user record
    user := &User{
        ID:       generateUUID(),
        KindeID:  claims.Sub,
        Email:    claims.Email,
        Name:     claims.Name,
        OrgID:    org.ID,
        CreatedAt: time.Now(),
    }
    
    if err := tx.Create(user).Error; err != nil {
        http.Error(w, "Failed to create user", http.StatusInternalServerError)
        return
    }
    
    tx.Commit()
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]interface{}{
        "organization": org,
        "user": user,
    })
}
```

## Roles & permissions

See [`docs/rbac-design.md`](./rbac-design.md) for the implemented model. Roles live in the `memberships` table (one row per (user, organization)), not on the `users` row.

## Complete User Journey

### New User Flow
1. **User visits AxiaOps** → Clicks "Sign Up"
2. **Kinde registration** → Email/password or social login
3. **Email verification** → User confirms email (if required)
4. **Return to AxiaOps** → Kinde redirects with auth code
5. **Token exchange** → AxiaOps gets JWT token
6. **New user check** → No existing user found
7. **Onboarding form** → User enters organization name
8. **Organization creation** → Backend creates org + user records
9. **Dashboard redirect** → User can now connect AWS accounts

### Existing User Flow
1. **User visits AxiaOps** → Clicks "Sign In"
2. **Kinde login** → Existing credentials
3. **Return to AxiaOps** → Kinde redirects with auth code
4. **Token exchange** → AxiaOps gets JWT token
5. **Existing user check** → User found in database
6. **Dashboard redirect** → Direct access to main application

## Error Handling

### Common Error Scenarios
- **Duplicate organization creation**: User refreshes onboarding page
- **Invalid organization name**: Too short, too long, or empty
- **Database errors**: Connection issues, constraint violations
- **Token validation failures**: Expired or invalid JWT

### Error Responses
```go
// Standard error response format
type ErrorResponse struct {
    Error   string `json:"error"`
    Code    string `json:"code"`
    Details string `json:"details,omitempty"`
}

// Example error responses
var (
    ErrUserExists = ErrorResponse{
        Error: "User already has an organization",
        Code:  "USER_EXISTS",
    }
    
    ErrInvalidOrgName = ErrorResponse{
        Error: "Organization name must be 2-100 characters",
        Code:  "INVALID_ORG_NAME",
    }
)
```

## Security Considerations

### Data Isolation
- **Kinde organization ID** used for RLS policies
- **JWT validation** on every request
- **CSRF protection** for state-changing operations

### Input Validation
- **Organization name**: Length limits, sanitization
- **Email validation**: Handled by Kinde
- **SQL injection prevention**: Parameterized queries

## Testing

### Unit Tests
```go
func TestCreateOrganization(t *testing.T) {
    // Test successful organization creation
    // Test duplicate user handling
    // Test invalid input validation
    // Test database error handling
}
```

### Integration Tests
```go
func TestOnboardingFlow(t *testing.T) {
    // Test complete flow from Kinde callback to dashboard
    // Test RLS policy enforcement
    // Test error scenarios
}
```

## Configuration

### Environment Variables
```bash
# Kinde configuration
KINDE_DOMAIN=https://axiaops.kinde.com
KINDE_CLIENT_ID=your_client_id
KINDE_CLIENT_SECRET=your_client_secret
KINDE_REDIRECT_URI=https://app.axiaops.com/auth/callback

# Database
DATABASE_URL=postgres://user:pass@host:port/dbname

# Application
ENCRYPTION_KEY=32_byte_hex_key_for_secrets
```

This onboarding flow ensures a smooth user experience while maintaining proper security and data isolation through Kinde's multi-tenant architecture.