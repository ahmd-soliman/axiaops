import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchResources } from '../api/client';
import { queryClient } from '../main';
import { useTheme } from '../theme/ThemeContext';
import DetailScreen from '../screens/DetailScreen';
import { Spinner } from '../components/primitives';
import NotFound from './NotFound';

export default function Detail() {
  const navigate = useNavigate();
  const { id } = useParams();
  const [params] = useSearchParams();
  const { theme } = useTheme();
  const account = params.get('account');
  const region  = params.get('region');
  const service = params.get('service');

  const { data: resources, isLoading } = useQuery({
    queryKey: ['resources', account],
    queryFn: () => fetchResources(account),
  });

  const zombie = resources?.find(
    (r) => r.resource_id === id && r.service === service && r.region === region
  );

  const goBack = () => navigate(-1);

  if (isLoading) {
    return (
      <div style={{ flex: 1, display: 'flex', justifyContent: 'center', alignItems: 'center', backgroundColor: theme.bg, minHeight: '100vh' }}>
        <Spinner />
      </div>
    );
  }

  if (!zombie) return <NotFound />;

  return (
    <DetailScreen
      zombie={zombie}
      onBack={goBack}
      onDismissed={() => {
        queryClient.invalidateQueries({ queryKey: ['resources'] });
        queryClient.invalidateQueries({ queryKey: ['zombies'] });
      }}
    />
  );
}
