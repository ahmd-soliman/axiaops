import React, { useState } from 'react';
import { SafeAreaView, StatusBar, StyleSheet } from 'react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import DashboardScreen from './src/screens/DashboardScreen';
import DetailScreen from './src/screens/DetailScreen';

const queryClient = new QueryClient();

export default function App() {
  const [selectedGhost, setSelectedGhost] = useState(null);

  return (
    <QueryClientProvider client={queryClient}>
      <SafeAreaView style={styles.container}>
        <StatusBar barStyle="light-content" backgroundColor="#0F172A" />
        {selectedGhost ? (
          <DetailScreen ghost={selectedGhost} onBack={() => setSelectedGhost(null)} />
        ) : (
          <DashboardScreen onSelectGhost={setSelectedGhost} />
        )}
      </SafeAreaView>
    </QueryClientProvider>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#0F172A' },
});
