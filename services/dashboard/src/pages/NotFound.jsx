import { useNavigate } from 'react-router-dom';

export default function NotFound() {
  const navigate = useNavigate();
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', minHeight: '100vh', backgroundColor: '#0F172A', color: '#F8FAFC', gap: 16 }}>
      <span style={{ fontSize: 48 }}>404</span>
      <span style={{ fontSize: 16, color: '#94A3B8' }}>Page not found</span>
      <button
        onClick={() => navigate('/')}
        style={{ marginTop: 8, padding: '10px 24px', borderRadius: 8, backgroundColor: '#F97316', color: '#fff', border: 'none', cursor: 'pointer', fontWeight: 700, fontSize: 14 }}
      >
        Go home
      </button>
    </div>
  );
}
