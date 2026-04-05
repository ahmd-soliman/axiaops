// Kinde PKCE OAuth flow using expo-auth-session.
// Works on web (redirect) and native (in-app browser tab).
import * as AuthSession from 'expo-auth-session';
import * as WebBrowser from 'expo-web-browser';

// Required so expo-auth-session can complete the session on web after redirect.
WebBrowser.maybeCompleteAuthSession();

const ISSUER = process.env.EXPO_PUBLIC_KINDE_ISSUER;
const CLIENT_ID = process.env.EXPO_PUBLIC_KINDE_CLIENT_ID;

// On web the redirect goes back to the running app.
// On native it goes to the Expo dev client deep link.
export function useKindeAuth() {
  const redirectUri = AuthSession.makeRedirectUri({ useProxy: false });

  const discovery = AuthSession.useAutoDiscovery(ISSUER);

  const [request, response, promptAsync] = AuthSession.useAuthRequest(
    {
      clientId: CLIENT_ID,
      redirectUri,
      scopes: ['openid', 'profile', 'email'],
      usePKCE: true,
    },
    discovery,
  );

  return { request, response, promptAsync, redirectUri, discovery };
}
