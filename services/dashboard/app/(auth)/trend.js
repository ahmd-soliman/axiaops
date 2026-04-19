import React from 'react';
import { useRouter } from 'expo-router';

import TrendScreen from '../../src/screens/TrendScreen';

export default function Trend() {
  const router = useRouter();

  function goBack() {
    if (router.canGoBack()) {
      router.back();
    } else {
      router.replace('/');
    }
  }

  return <TrendScreen onBack={goBack} />;
}
