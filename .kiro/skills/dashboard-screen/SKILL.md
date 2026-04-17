---
name: dashboard-screen
description: "Create or modify a screen in the AxiaOps React Native (Expo) dashboard. Use this skill when someone wants to add a new page, screen, or view to the frontend, modify an existing screen like DashboardScreen or ConnectScreen, add UI components, integrate API calls, or work with the dashboard navigation. Also trigger for mentions of 'dashboard UI', 'frontend screen', 'Expo component', 'React Native view', or anything about the AxiaOps web app interface."
---

# Dashboard Screen Skill

The AxiaOps dashboard is a React Native (Expo) web app served via nginx. It talks to the API service through an nginx reverse proxy (`/api/*` → api:8080).

## Before You Start

Familiarize yourself with the existing codebase:

- `services/dashboard/src/screens/` — existing screens (DashboardScreen, DetailScreen, ConnectScreen)
- `services/dashboard/src/api/client.js` — API client with auth token handling
- `services/dashboard/src/components/` — reusable UI components
- `services/dashboard/src/components/serviceConfig.js` — per-service display config (icons, colors)
- `services/dashboard/package.json` — dependencies (React 19, TanStack Query, Kinde auth)
- `services/dashboard/CLAUDE.md` — frontend-specific conventions

## Tech Stack

- **React Native** with **Expo** (web target — not mobile)
- **TanStack Query** (`@tanstack/react-query`) for data fetching and caching
- **Kinde** for authentication (skipped in dev mode via `EXPO_PUBLIC_DEV_MODE=true`)
- **React Navigation** for screen routing
- **StyleSheet** from React Native for styling (not CSS, not Tailwind)

## Screen Template

```jsx
import React from 'react';
import { View, Text, ScrollView, StyleSheet, ActivityIndicator } from 'react-native';
import { useQuery } from '@tanstack/react-query';
import { fetchSomething } from '../api/client';

export default function NewScreen({ navigation }) {
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['something'],
    queryFn: fetchSomething,
  });

  if (isLoading) {
    return (
      <View style={styles.center}>
        <ActivityIndicator size="large" color="#4F46E5" />
      </View>
    );
  }

  if (error) {
    return (
      <View style={styles.center}>
        <Text style={styles.error}>Failed to load data</Text>
      </View>
    );
  }

  return (
    <ScrollView style={styles.container}>
      <Text style={styles.title}>Screen Title</Text>
      {/* Content here */}
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#F8F9FA',
    padding: 20,
  },
  center: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
  },
  title: {
    fontSize: 24,
    fontWeight: '700',
    color: '#1a1a2e',
    marginBottom: 16,
  },
  error: {
    color: '#DC2626',
    fontSize: 16,
  },
});
```

## Adding an API Call

All API calls go through `services/dashboard/src/api/client.js`. The client handles auth tokens and base URL:

```js
// In client.js — add a new fetch function
export async function fetchNewThing() {
  const res = await apiFetch('/v1/new-thing');
  return res.json();
}
```

The `apiFetch` wrapper automatically includes the Bearer token and handles auth refresh. Use it for all API calls — never use raw `fetch` to the API.

## Navigation

Screens are registered in the app's navigation config. When adding a new screen:

1. Create the screen file in `services/dashboard/src/screens/`
2. Register it in the navigation stack (check `App.js` or the navigation config file)
3. Navigate to it from other screens: `navigation.navigate('NewScreen', { params })`

## Design Conventions

- **Primary color:** `#4F46E5` (indigo)
- **Background:** `#F8F9FA` (light gray)
- **Text:** `#1a1a2e` (dark navy)
- **Error:** `#DC2626` (red)
- **Success/savings:** `#16A34A` (green)
- **Cards:** white background, subtle shadow, rounded corners (borderRadius: 12)
- **Loading states:** always show `ActivityIndicator` — never leave blank screens
- **Error states:** show a clear message with option to retry via `refetch()`

## Checklist for a New Screen

- [ ] Screen component created in `services/dashboard/src/screens/`
- [ ] API function added to `client.js` (if calling new endpoints)
- [ ] TanStack Query hook wired up for data fetching
- [ ] Loading and error states handled
- [ ] Screen registered in navigation
- [ ] StyleSheet used for all styling (no inline styles beyond trivial cases)
- [ ] Tested in browser via `make start-dev` (dashboard runs at localhost:80)
