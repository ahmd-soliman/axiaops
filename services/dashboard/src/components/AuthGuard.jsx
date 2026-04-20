import { Outlet, Navigate } from 'react-router-dom';
import { getToken } from '../auth/storage';

export default function AuthGuard() {
  return getToken() ? <Outlet /> : <Navigate to="/login" replace />;
}
