import { useNavigate } from 'react-router-dom';
import { queryClient } from '../main';
import ConnectScreen from '../screens/ConnectScreen';

export default function Connect() {
  const navigate = useNavigate();
  return (
    <ConnectScreen
      onConnected={() => {
        queryClient.invalidateQueries({ queryKey: ['accounts'] });
        navigate('/', { replace: true });
      }}
      onSkip={() => navigate('/', { replace: true })}
      onCancel={() => navigate(-1)}
    />
  );
}
