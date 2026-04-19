import { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { getKindeClient } from '../auth/kinde';
import { saveToken } from '../auth/storage';
import { setAuthToken } from '../api/client';

export default function Callback() {
  const navigate = useNavigate();

  useEffect(() => {
    (async () => {
      try {
        const client = await getKindeClient();
        await client.handleRedirectCallback();
        const token = await client.getToken();
        saveToken(token);
        setAuthToken(token);
        navigate('/', { replace: true });
      } catch (e) {
        console.error('Auth callback failed:', e);
        navigate('/login', { replace: true });
      }
    })();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  return null;
}
