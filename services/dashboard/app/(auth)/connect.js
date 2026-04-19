import React from 'react';
import { useRouter } from 'expo-router';

import ConnectScreen from '../../src/screens/ConnectScreen';
import { queryClient } from '../_layout';

export default function Connect() {
  const router = useRouter();

  function goBack() {
    if (router.canGoBack()) {
      router.back();
    } else {
      router.replace('/');
    }
  }

  function handleConnected() {
    queryClient.invalidateQueries();
    router.replace('/');
  }

  return (
    <ConnectScreen
      onConnected={handleConnected}
      onSkip={goBack}
      onCancel={goBack}
    />
  );
}
