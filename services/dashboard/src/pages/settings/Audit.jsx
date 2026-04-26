import AuditScreen from '../../screens/AuditScreen';

// Audit log tab. The screen manages its own filter state via URL-less React
// state — filters are transient enough that deep-linking isn't worth the
// pagination-cursor-in-URL complexity yet.
export default function Audit() {
  return <AuditScreen />;
}
