import { useNavigate } from 'react-router-dom';

export default function NotFound() {
  const navigate = useNavigate();
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', minHeight: '100vh', backgroundColor: '#0B1220', color: '#E5ECF5', gap: 16 }}>
      <span style={{ fontSize: 48 }}>404</span>
      <span style={{ fontSize: 16, color: '#8497B2' }}>Page not found</span>
      <button
        onClick={() => navigate('/')}
        style={{ marginTop: 8, padding: '10px 24px', borderRadius: 8, backgroundColor: '#4F46E5', color: '#fff', border: 'none', cursor: 'pointer', fontWeight: 700, fontSize: 14 }}
      >
        Go home
      </button>
    </div>
  );
}
