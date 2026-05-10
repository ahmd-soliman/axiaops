import { useNavigate } from 'react-router-dom';
import { useTheme } from '../theme/ThemeContext';

export default function NotFound() {
  const navigate = useNavigate();
  const { theme: t } = useTheme();
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', minHeight: '100vh', backgroundColor: t.bg, color: t.text, gap: 16 }}>
      <span style={{ fontSize: 48 }}>404</span>
      <span style={{ fontSize: 16, color: t.textMuted }}>Page not found</span>
      <button
        onClick={() => navigate('/')}
        style={{ marginTop: 8, padding: '10px 24px', borderRadius: 8, backgroundColor: t.accent, color: t.textOnDark, border: 'none', cursor: 'pointer', fontWeight: 700, fontSize: 14 }}
      >
        Go home
      </button>
    </div>
  );
}
