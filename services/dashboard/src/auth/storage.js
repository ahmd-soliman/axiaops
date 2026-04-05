// Cross-platform token storage.
// expo-secure-store only works on native (iOS/Android).
// On web we fall back to localStorage.
import { Platform } from 'react-native';
import * as SecureStore from 'expo-secure-store';

const KEY = 'axiaops_token';

export async function saveToken(token) {
  if (Platform.OS === 'web') {
    localStorage.setItem(KEY, token);
  } else {
    await SecureStore.setItemAsync(KEY, token);
  }
}

export async function getToken() {
  if (Platform.OS === 'web') {
    return localStorage.getItem(KEY);
  }
  return SecureStore.getItemAsync(KEY);
}

export async function clearToken() {
  if (Platform.OS === 'web') {
    localStorage.removeItem(KEY);
  } else {
    await SecureStore.deleteItemAsync(KEY);
  }
}
