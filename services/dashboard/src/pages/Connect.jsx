import { useNavigate } from 'react-router-dom';
import { queryClient } from '../main';
import ConnectScreen from '../screens/ConnectScreen';

export default function Connect() {
  const navigate = useNavigate();
  const goToDashboard = () => navigate('/?skip_connect=1', { replace: true });

  return (
    <ConnectScreen
      onConnected={() => {
        queryClient.invalidateQueries({ queryKey: ['accounts'] });
        navigate('/', { replace: true });
      }}
      onSkip={goToDashboard}
      onCancel={goToDashboard}
    />
  );
}
