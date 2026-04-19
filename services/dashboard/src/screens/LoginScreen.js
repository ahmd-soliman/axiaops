import React from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  ActivityIndicator,
  StyleSheet,
} from 'react-native';
import { useTheme } from '../theme/ThemeContext';

export default function LoginScreen({ onLogin, loading }) {
  const { theme } = useTheme();
  const styles = createStyles(theme);

  return (
    <View style={styles.container}>
      <View style={styles.card}>
        {/* Logo mark */}
        <View style={styles.logoMark}>
          <Text style={styles.logoMarkText}>⚡</Text>
        </View>

        <Text style={styles.logo}>AxiaOps</Text>
        <Text style={styles.tagline}>
          Spot idle cloud resources.{'\n'}Cut costs automatically.
        </Text>

        <TouchableOpacity
          style={[styles.button, loading && styles.buttonDisabled]}
          onPress={onLogin}
          disabled={loading}
          activeOpacity={0.85}
        >
          {loading ? (
            <ActivityIndicator color="#fff" />
          ) : (
            <Text style={styles.buttonText}>Sign in to continue</Text>
          )}
        </TouchableOpacity>

        <Text style={styles.hint}>
          Secure sign-in via Kinde. No password stored here.
        </Text>
      </View>
    </View>
  );
}

const createStyles = (theme) => StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: theme.bg,
    alignItems: 'center',
    justifyContent: 'center',
    padding: 24,
  },
  card: {
    width: '100%',
    maxWidth: 400,
    backgroundColor: theme.surface,
    borderRadius: 20,
    padding: 40,
    alignItems: 'center',
    gap: 14,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: theme.border,
  },
  logoMark: {
    width: 56,
    height: 56,
    borderRadius: 16,
    backgroundColor: theme.accentLight,
    alignItems: 'center',
    justifyContent: 'center',
    marginBottom: 4,
  },
  logoMarkText: { fontSize: 26 },
  logo: {
    fontSize: 30,
    fontWeight: '800',
    color: theme.accent,
    letterSpacing: -0.5,
  },
  tagline: {
    fontSize: 15,
    color: theme.textMid,
    textAlign: 'center',
    lineHeight: 23,
    marginBottom: 6,
  },
  button: {
    backgroundColor: theme.accent,
    borderRadius: 12,
    paddingVertical: 15,
    paddingHorizontal: 48,
    width: '100%',
    alignItems: 'center',
    marginTop: 6,
  },
  buttonDisabled: { opacity: 0.6 },
  buttonText: {
    color: '#fff',
    fontSize: 16,
    fontWeight: '700',
  },
  hint: {
    fontSize: 12,
    color: theme.textMuted,
    textAlign: 'center',
    lineHeight: 18,
  },
});
