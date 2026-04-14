import * as AuthSession from 'expo-auth-session';
import * as WebBrowser from 'expo-web-browser';
import { KINDE_ISSUER, KINDE_CLIENT_ID } from '../config';

WebBrowser.maybeCompleteAuthSession();

export function useKindeAuth() {
  const redirectUri = AuthSession.makeRedirectUri({ useProxy: false });
  const discovery = AuthSession.useAutoDiscovery(KINDE_ISSUER);
  const [request, response, promptAsync] = AuthSession.useAuthRequest(
    { clientId: KINDE_CLIENT_ID, redirectUri, scopes: ['openid', 'profile', 'email'], usePKCE: true },
    discovery,
  );
  return { request, response, promptAsync, redirectUri, discovery };
}
