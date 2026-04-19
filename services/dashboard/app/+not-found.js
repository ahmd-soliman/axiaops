import React from 'react';
import { View, Text, TouchableOpacity, StyleSheet } from 'react-native';
import { useRouter } from 'expo-router';
import { useTheme } from '../src/theme/ThemeContext';

export default function NotFound() {
  const router = useRouter();
  const { theme } = useTheme();

  return (
    <View style={[styles.container, { backgroundColor: theme.bg }]}>
      <Text style={[styles.code, { color: theme.accent }]}>404</Text>
      <Text style={[styles.title, { color: theme.text }]}>Page not found</Text>
      <Text style={[styles.subtitle, { color: theme.textMuted }]}>
        The resource you're looking for doesn't exist or has been removed.
      </Text>
      <TouchableOpacity
        style={[styles.button, { backgroundColor: theme.accent }]}
        onPress={() => router.replace('/')}
      >
        <Text style={[styles.buttonText, { color: theme.textOnDark }]}>
          Back to Dashboard
        </Text>
      </TouchableOpacity>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    padding: 32,
  },
  code: {
    fontSize: 72,
    fontWeight: '800',
    marginBottom: 8,
  },
  title: {
    fontSize: 24,
    fontWeight: '700',
    marginBottom: 8,
  },
  subtitle: {
    fontSize: 15,
    textAlign: 'center',
    marginBottom: 32,
    lineHeight: 22,
  },
  button: {
    paddingHorizontal: 24,
    paddingVertical: 12,
    borderRadius: 8,
  },
  buttonText: {
    fontSize: 15,
    fontWeight: '600',
  },
});
