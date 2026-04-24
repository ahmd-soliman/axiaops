import AuditScreen from '../screens/AuditScreen';

// Audit log page. The screen manages its own filter state via URL-less React
// state — filters are transient enough that deep-linking isn't worth the
// pagination-cursor-in-URL complexity yet. Revisit once Phase 3.9 roles land
// and the screen becomes visible to non-admin users.
export default function Audit() {
  return <AuditScreen />;
}
