import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchResources, fetchDismissals } from '../api/client';
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
  // /v1/resources doesn't enrich with dismissal_id today (only /v1/zombies does
  // via enrichWithDismissals in the api). Pulling dismissals here lets us
  // annotate the resource client-side so DetailScreen shows Restore (not the
  // Dismiss/Snooze buttons) for dismissed/snoozed resources reached from the
  // Hidden view's clickable cards.
  const { data: dismissals } = useQuery({
    queryKey: ['dismissals', account],
    queryFn: () => fetchDismissals(account),
  });

  const found = resources?.find(
    (r) => r.resource_id === id && r.service === service && r.region === region
  );

  const dismissalMatch = found && dismissals?.find(
    (d) =>
      d.account_id === found.internal_account_id &&
      d.provider === found.provider &&
      d.service === found.service &&
      d.region === found.region &&
      d.resource_id === found.resource_id,
  );

  const zombie = found
    ? (dismissalMatch ? { ...found, dismissal_id: dismissalMatch.id, dismiss_action: dismissalMatch.action } : found)
    : null;

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
        queryClient.invalidateQueries({ queryKey: ['dismissals'] });
      }}
    />
  );
}
