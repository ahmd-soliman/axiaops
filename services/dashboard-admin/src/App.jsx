import { Navigate, NavLink, Route, Routes, useLocation } from 'react-router-dom';
import { hasRole, useAdminAuth } from './auth/AdminAuth.jsx';
import { BrandLogo, ThemeToggle } from './Brand.jsx';
import LoginScreen from './screens/LoginScreen.jsx';
import TenantsScreen from './screens/TenantsScreen.jsx';
import TenantDetailScreen from './screens/TenantDetailScreen.jsx';
import StaffScreen from './screens/StaffScreen.jsx';

// TopBar — only shown when authenticated. The Staff link is hidden for
// non-superadmins (the backend enforces it regardless; this just declutters).
function TopBar() {
  const { staff, logout } = useAdminAuth();
  return (
    <header className="topbar">
      <BrandLogo />
      <span className="tag">admin</span>
      <nav>
        <NavLink to="/tenants" className={({ isActive }) => (isActive ? 'active' : '')}>
          Tenants
        </NavLink>
        {hasRole(staff, 'superadmin') && (
          <NavLink to="/staff" className={({ isActive }) => (isActive ? 'active' : '')}>
            Staff
          </NavLink>
        )}
      </nav>
      <span className="spacer" />
      <span className="who">
        {staff?.name || staff?.email} · {staff?.roles?.join(', ')}
      </span>
      <ThemeToggle />
      <button className="secondary" onClick={logout}>
        Sign out
      </button>
    </header>
  );
}

// RequireAuth gates the authenticated surface. While the initial /admin/me
// probe is in flight we render nothing (avoids a login-screen flash for an
// already-authenticated refresh); unauthenticated → redirect to /login.
function RequireAuth({ children }) {
  const { staff, loading } = useAdminAuth();
  const location = useLocation();
  if (loading) return null;
  if (!staff) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  return children;
}

function Shell({ children }) {
  return (
    <>
      <TopBar />
      <main className="container">{children}</main>
    </>
  );
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginScreen />} />
      <Route
        path="/tenants"
        element={
          <RequireAuth>
            <Shell>
              <TenantsScreen />
            </Shell>
          </RequireAuth>
        }
      />
      <Route
        path="/tenants/:id"
        element={
          <RequireAuth>
            <Shell>
              <TenantDetailScreen />
            </Shell>
          </RequireAuth>
        }
      />
      <Route
        path="/staff"
        element={
          <RequireAuth>
            <Shell>
              <StaffScreen />
            </Shell>
          </RequireAuth>
        }
      />
      <Route path="*" element={<Navigate to="/tenants" replace />} />
    </Routes>
  );
}
