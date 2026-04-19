import createKindeClient from '@kinde-oss/kinde-auth-pkce-js';
import { KINDE_ISSUER, KINDE_CLIENT_ID } from '../config';

// Lazy singleton — the SDK returns a Promise. All callers await this.
let clientPromise = null;

export function getKindeClient() {
  if (!clientPromise) {
    clientPromise = createKindeClient({
      domain: KINDE_ISSUER,
      client_id: KINDE_CLIENT_ID,
      redirect_uri: window.location.origin + '/callback',
      logout_uri: window.location.origin + '/login',
      scope: 'openid profile email',
    });
  }
  return clientPromise;
}
