import { useNavigate } from 'react-router-dom';
import TrendScreen from '../screens/TrendScreen';

export default function Trend() {
  const navigate = useNavigate();
  return <TrendScreen onBack={() => navigate(-1)} />;
}
