import React from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  ActivityIndicator,
  StyleSheet,
} from 'react-native';

const C = {
  bg: '#0F172A',
  navyMid: '#1E293B',
  accent: '#F97316',
  textMuted: '#94A3B8',
  white: '#FFFFFF',
};

export default function LoginScreen({ onLogin, loading }) {
  return (
    <View style={styles.container}>
      <View style={styles.card}>
        <Text style={styles.logo}>AxiaOps</Text>
        <Text style={styles.tagline}>Find idle cloud resources.{'\n'}Stop paying for nothing.</Text>

        <TouchableOpacity
          style={[styles.button, loading && styles.buttonDisabled]}
          onPress={onLogin}
          disabled={loading}
          activeOpacity={0.85}
        >
          {loading ? (
            <ActivityIndicator color={C.white} />
          ) : (
            <Text style={styles.buttonText}>Sign in</Text>
          )}
        </TouchableOpacity>

        <Text style={styles.hint}>You will be redirected to Kinde to authenticate.</Text>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: C.bg,
    alignItems: 'center',
    justifyContent: 'center',
    padding: 24,
  },
  card: {
    width: '100%',
    maxWidth: 400,
    backgroundColor: C.navyMid,
    borderRadius: 16,
    padding: 40,
    alignItems: 'center',
    gap: 16,
  },
  logo: {
    fontSize: 32,
    fontWeight: '800',
    color: C.accent,
    letterSpacing: -1,
    marginBottom: 4,
  },
  tagline: {
    fontSize: 16,
    color: C.white,
    textAlign: 'center',
    lineHeight: 24,
    marginBottom: 8,
  },
  button: {
    backgroundColor: C.accent,
    borderRadius: 10,
    paddingVertical: 14,
    paddingHorizontal: 48,
    width: '100%',
    alignItems: 'center',
    marginTop: 8,
  },
  buttonDisabled: {
    opacity: 0.6,
  },
  buttonText: {
    color: C.white,
    fontSize: 16,
    fontWeight: '700',
  },
  hint: {
    fontSize: 12,
    color: C.textMuted,
    textAlign: 'center',
    marginTop: 4,
  },
});
