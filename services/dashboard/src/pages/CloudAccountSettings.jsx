import { useNavigate, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchAccounts } from '../api/client';
import { queryClient } from '../main';
import AccountSettingsScreen from '../screens/AccountSettingsScreen';
import { Spinner } from '../components/primitives';
import { useTheme } from '../theme/ThemeContext';

export default function CloudAccountSettings() {
  const navigate = useNavigate();
  const { accountId } = useParams();
  const { theme } = useTheme();

  const { data: accounts, isLoading } = useQuery({
    queryKey: ['accounts'],
    queryFn: fetchAccounts,
  });

  const account = accounts?.find((a) => a.id === accountId);

  if (isLoading) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '100vh', backgroundColor: theme.bg }}>
        <Spinner />
      </div>
    );
  }

  if (!account) {
    navigate('/settings/cloud-accounts', { replace: true });
    return null;
  }

  return (
    <AccountSettingsScreen
      account={account}
      onBack={() => navigate(-1)}
      onAccountUpdated={() => {
        queryClient.invalidateQueries({ queryKey: ['accounts'] });
        navigate('/settings/cloud-accounts', { replace: true });
      }}
      onAccountDeleted={() => {
        queryClient.invalidateQueries({ queryKey: ['accounts'] });
        navigate('/settings/cloud-accounts', { replace: true });
      }}
    />
  );
}
