# React + Vite Migration Plan

## Goal

Replace the Expo / Metro / React Native Web stack with plain React + Vite. The app
targets web only; the React Native overhead (duplicate-React issues, Metro slowness,
React Native Web abstraction layer) is not worth the cost for a web-first FinOps SaaS.

## What changes vs. what stays

| Layer | Current | After |
|---|---|---|
| Build tool | Metro / `npx expo export` | Vite |
| Entry | `expo-router/entry` → `app/` | `src/main.jsx` |
| Routing | expo-router (file-based) | React Router v6 |
| Primitives | `View`, `Text`, `TouchableOpacity`, etc. | `div`, `span`, `button`, etc. |
| Styles | `StyleSheet.create({ … })` | Plain JS objects (same values, no wrapper) |
| Auth | `expo-auth-session` + `expo-web-browser` | `@kinde-oss/kinde-auth-pkce-js` |
| Token storage | `expo-secure-store` / `localStorage` | `localStorage` only |
| Theme | `ThemeContext` (kept as-is) | `ThemeContext` (kept as-is) |
| API client | `src/api/client.js` | Same file, one-line env var rename (`EXPO_PUBLIC_API_URL` → `VITE_API_URL`) |
| Data fetching | TanStack Query (kept as-is) | TanStack Query (kept as-is) |
| Env vars | `EXPO_PUBLIC_*` | `VITE_*` |
| Docker build | `npx expo export --platform web` | `npm run build` (vite build) |

**Nothing changes in the backend.** No API changes needed.

## Styling strategy

Do **not** introduce Tailwind or CSS Modules during this migration. The current
approach — inline style objects with theme tokens from `useTheme()` — works
perfectly fine on the web. The only change is removing the `StyleSheet.create()`
wrapper:

```js
// Before
const styles = StyleSheet.create({ container: { flex: 1, backgroundColor: theme.bg } });

// After
const styles = { container: { flex: 1, backgroundColor: theme.bg } };
```

This keeps the diff in every component file minimal and avoids CSS class-name
collisions. A CSS/Tailwind cleanup can be a separate, optional follow-up.

**Two non-trivial gotchas** that the primitives in Task 2 absorb so screen code
stays untouched:

1. **RN `View` defaults to `display:flex; flex-direction:column`; `<div>` does
   not.** Layouts that rely on implicit column flex or on `flex:1` children
   filling a parent will silently break if you swap `<View>` → `<div>`. The
   `<View>` primitive in Task 2 sets these defaults so screens render correctly
   without editing every container.
2. **RN accepts `style={[a, b && c]}` arrays; React DOM does not.** The codebase
   uses this pattern in ~70 places. The primitives flatten arrays via a shared
   `flatStyle()` helper; plain HTML elements in screen code must be converted to
   a primitive or the style array must be collapsed manually.

Visual output will be close but not byte-identical — expect a short
spot-the-difference pass per screen.

## React Native → HTML component map

| React Native | HTML / React DOM |
|---|---|
| `<View>` | `<View>` primitive (see Task 2) — a `<div>` with RN's `display:flex; flex-direction:column` defaults baked in |
| `<Text>` | `<Text>` primitive (see Task 2) — a `<span style={{ display: 'block' }}>` to avoid inline layout surprises |
| `<TouchableOpacity onPress={f}>` | `<Pressable>` primitive (see Task 2) — a reset `<button>` with RN-style behavior |
| `<ScrollView>` | `<div style={{ overflowY: 'auto' }}>` |
| `<FlatList data={d} renderItem={f}>` | `d.map(f)` inside a `<div>` |
| `<TextInput value={v} onChangeText={f}>` | `<input value={v} onChange={e => f(e.target.value)}>` |
| `<TextInput multiline>` | `<textarea>` |
| `<ActivityIndicator>` | Small spinner `<div>` component (see Task 2) |
| `<Modal visible={b}>` | Conditional render of a `<div>` overlay (see Task 2) |
| `<RefreshControl>` | Drop entirely — pull-to-refresh is a deliberate UX regression on web; expose React Query's `refetch()` via an explicit refresh button |
| `<Alert.alert(title, msg)>` | `window.confirm(msg)` or a custom modal |
| `useWindowDimensions()` | `useEffect` + `window.addEventListener('resize', …)` |
| `StyleSheet.create({ … })` | `const styles = { … }` |
| `Platform.OS` checks | Remove — web only |

## New dependency list

```
# Keep
react, react-dom
@tanstack/react-query
vite (new)
@vitejs/plugin-react (new)
react-router-dom (new, replaces expo-router)
@kinde-oss/kinde-auth-pkce-js (new, replaces expo-auth-session + expo-web-browser)

# Remove
expo, expo-router, expo-auth-session, expo-web-browser, expo-secure-store
expo-status-bar, expo-linking, expo-constants, expo-crypto
react-native, react-native-web, react-native-safe-area-context, react-native-screens
@expo/metro-runtime
```

---

## Task Breakdown

### Task 1 — Scaffold the Vite project

**Create a new `services/dashboard-v2/` directory alongside the existing one.**
Working in a sibling directory means the current dashboard keeps running in Docker
while you migrate screen by screen.

```bash
cd services/
npm create vite@latest dashboard-v2 -- --template react
cd dashboard-v2
npm install
npm install react-router-dom @tanstack/react-query @kinde-oss/kinde-auth-pkce-js
```

**Update `vite.config.js`** to proxy `/api` to the local API server (mirrors nginx in
prod, so no CORS in dev):

```js
// services/dashboard-v2/vite.config.js
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
    },
  },
});
```

**Copy files from `dashboard/src/`, with small edits where noted:**
- `src/api/client.js` — copy, then change `EXPO_PUBLIC_API_URL` → `import.meta.env.VITE_API_URL` (one line; `process.env` is not available under Vite)
- `src/components/serviceConfig.js` — copy unchanged
- `src/theme/ThemeContext.js` — copy unchanged (already web-clean, localStorage-backed)
- `src/config.js` — rewrite to use `import.meta.env.VITE_*` (full replacement below)

**Rewrite `src/config.js`:**

```js
// services/dashboard-v2/src/config.js
const env = window.__ENV__ || {};

export const DEV_MODE       = (env.DEV_MODE       ?? import.meta.env.VITE_DEV_MODE)       === 'true';
export const KINDE_ISSUER   = env.KINDE_ISSUER    ?? import.meta.env.VITE_KINDE_ISSUER    ?? '';
export const KINDE_CLIENT_ID = env.KINDE_CLIENT_ID ?? import.meta.env.VITE_KINDE_CLIENT_ID ?? '';
export const DEV_ORG_NAME   = env.DEV_ORG_NAME    ?? import.meta.env.VITE_DEV_ORG_NAME    ?? 'AxiaOps Dev';
```

**Create `index.html`** in the project root (Vite's entry point):

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>AxiaOps</title>
    <script>window.__ENV__ = {};</script>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.jsx"></script>
  </body>
</html>
```

---

### Task 2 — Shared primitive components

Create `src/components/primitives/` with thin wrappers that match the React Native
API surface. This is the most important task — it makes every screen conversion
mechanical. Write these once, use everywhere.

**`src/components/primitives/index.js`** — re-exports all primitives plus the
`flatStyle` helper:

```js
// Flattens React Native–style `style={[a, b && c, [d, e]]}` arrays into a
// single object suitable for React DOM. Falsy entries are skipped.
export function flatStyle(style) {
  if (!style) return undefined;
  if (!Array.isArray(style)) return style;
  return Object.assign({}, ...style.flat(Infinity).filter(Boolean));
}

export { View } from './View';
export { Text } from './Text';
export { Pressable } from './Pressable';
export { Spinner } from './Spinner';
export { Overlay } from './Overlay';
export { useWindowWidth } from './useWindowWidth';
```

**`View.jsx`** — replaces `<View>`, preserves RN flex defaults:
```jsx
import { flatStyle } from './index';

export function View({ style, children, ...rest }) {
  return (
    <div
      {...rest}
      style={{ display: 'flex', flexDirection: 'column', ...flatStyle(style) }}
    >
      {children}
    </div>
  );
}
```

**`Text.jsx`** — replaces `<Text>`, avoids inline-layout surprises:
```jsx
import { flatStyle } from './index';

export function Text({ style, children, ...rest }) {
  return (
    <span {...rest} style={{ display: 'block', ...flatStyle(style) }}>
      {children}
    </span>
  );
}
```

**`Pressable.jsx`** — replaces `TouchableOpacity`:
```jsx
import { flatStyle } from './index';

export function Pressable({ onPress, style, children, disabled }) {
  return (
    <button
      onClick={onPress}
      disabled={disabled}
      style={{
        background: 'none', border: 'none', padding: 0,
        font: 'inherit', color: 'inherit', textAlign: 'inherit',
        cursor: disabled ? 'default' : 'pointer',
        display: 'flex', flexDirection: 'column',
        ...flatStyle(style),
      }}
    >
      {children}
    </button>
  );
}
```

**`Spinner.jsx`** — replaces `ActivityIndicator`:
```jsx
export function Spinner({ size = 24, color = '#F97316' }) {
  return (
    <div style={{ width: size, height: size, border: `2px solid ${color}33`,
      borderTopColor: color, borderRadius: '50%', animation: 'spin 0.7s linear infinite' }} />
  );
}
// Add `@keyframes spin { to { transform: rotate(360deg); } }` to index.css
```

**`Overlay.jsx`** — replaces `Modal`:
```jsx
export function Overlay({ visible, onClose, children }) {
  if (!visible) return null;
  return (
    <div style={{ position: 'fixed', inset: 0, backgroundColor: '#00000080',
      display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}
      onClick={onClose}>
      <div onClick={e => e.stopPropagation()}>{children}</div>
    </div>
  );
}
```

**`useWindowWidth.js`** — replaces `useWindowDimensions`:
```js
import { useState, useEffect } from 'react';

export function useWindowWidth() {
  const [width, setWidth] = useState(window.innerWidth);
  useEffect(() => {
    const handler = () => setWidth(window.innerWidth);
    window.addEventListener('resize', handler);
    return () => window.removeEventListener('resize', handler);
  }, []);
  return width;
}
```

These primitives (View, Text, Pressable, Spinner, Overlay, useWindowWidth) plus
the `flatStyle` helper cover the non-trivial React Native APIs used in the
codebase.

---

### Task 3 — Auth migration

Replace `expo-auth-session` / `expo-web-browser` with `@kinde-oss/kinde-auth-pkce-js`.

**Important: `createKindeClient()` is async and returns a Promise.** Treating it
as a synchronous singleton — `const kindeClient = createKindeClient({...})` —
produces a Promise, and every downstream `.login()` / `.handleRedirectCallback()`
call throws "is not a function." Bootstrap the client once, cache the Promise,
and `await` it at each call site.

**`src/auth/kinde.js`** — rewrite:

```js
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
```

**`src/auth/storage.js`** — simplify to web-only:

```js
const KEY = 'axiaops_token';
export const saveToken  = (t) => localStorage.setItem(KEY, t);
export const getToken   = ()  => localStorage.getItem(KEY);
export const clearToken = ()  => localStorage.removeItem(KEY);
```

**Add a `/callback` route** (see Task 5) that awaits `getKindeClient()`, calls
`client.handleRedirectCallback()`, then navigates to `/`.

**`login.jsx`** awaits `getKindeClient()` then calls `client.login()` — no hooks,
no PKCE boilerplate.

The Kinde SDK manages PKCE, code exchange, and token storage internally.
The `getToken()` in `storage.js` is kept for the auth guard and the API client;
after login you still call `saveToken(await client.getToken())` to mirror the
existing contract. This keeps the API client unchanged.

---

### Task 4 — Root layout and router

**`src/main.jsx`:**

```jsx
import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClientProvider, QueryClient } from '@tanstack/react-query';
import { ThemeProvider } from './theme/ThemeContext';
import App from './App';
import './index.css';

export const queryClient = new QueryClient();

ReactDOM.createRoot(document.getElementById('root')).render(
  <QueryClientProvider client={queryClient}>
    <ThemeProvider>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </ThemeProvider>
  </QueryClientProvider>
);
```

**`src/App.jsx`** — auth bootstrap + route table:

```jsx
import { useEffect, useState } from 'react';
import { Routes, Route, Navigate } from 'react-router-dom';
import { getToken, saveToken } from './auth/storage';
import { setAuthToken } from './api/client';
import { DEV_MODE, DEV_ORG_NAME } from './config';
import { getKindeClient } from './auth/kinde';

import AuthGuard   from './components/AuthGuard';
import Dashboard   from './pages/Dashboard';
import Detail      from './pages/Detail';
import Trend       from './pages/Trend';
import Connect     from './pages/Connect';
import Settings    from './pages/Settings';
import Login       from './pages/Login';
import Callback    from './pages/Callback';
import NotFound    from './pages/NotFound';

export default function App() {
  const [ready, setReady] = useState(false);

  useEffect(() => {
    if (DEV_MODE) {
      const payload = btoa(JSON.stringify({ org_name: DEV_ORG_NAME }));
      const devToken = `dev.${payload}.dev`;
      saveToken(devToken);
      setAuthToken(devToken);
      setReady(true);
      return;
    }
    // Warm up the Kinde client so Login/Callback can await a resolved Promise.
    // We don't need the return value here — we just want the singleton created.
    getKindeClient().catch(() => {}); // non-fatal; Login surfaces the error
    const stored = getToken();
    if (stored) setAuthToken(stored);
    setReady(true);
  }, []);

  if (!ready) return null;

  return (
    <Routes>
      <Route path="/login"    element={<Login />} />
      <Route path="/callback" element={<Callback />} />
      <Route element={<AuthGuard />}>
        <Route path="/"                      element={<Dashboard />} />
        <Route path="/detail/:id"            element={<Detail />} />
        <Route path="/trend"                 element={<Trend />} />
        <Route path="/connect"               element={<Connect />} />
        <Route path="/settings/:accountId"   element={<Settings />} />
      </Route>
      <Route path="*" element={<NotFound />} />
    </Routes>
  );
}
```

**`src/components/AuthGuard.jsx`:**

```jsx
import { Outlet, Navigate } from 'react-router-dom';
import { getToken } from '../auth/storage';

export default function AuthGuard() {
  return getToken() ? <Outlet /> : <Navigate to="/login" replace />;
}
```

---

### Task 5 — Page files (routes)

Each page is a thin wrapper — same pattern as the expo-router migration. No business
logic lives here.

**`src/pages/Callback.jsx`** — handles the Kinde redirect:

```jsx
import { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { getKindeClient } from '../auth/kinde';
import { saveToken } from '../auth/storage';
import { setAuthToken } from '../api/client';

export default function Callback() {
  const navigate = useNavigate();

  useEffect(() => {
    (async () => {
      try {
        const client = await getKindeClient();
        await client.handleRedirectCallback();
        const token = await client.getToken();
        saveToken(token);
        setAuthToken(token);
        navigate('/', { replace: true });
      } catch (e) {
        console.error('Auth callback failed:', e);
        navigate('/login', { replace: true });
      }
    })();
  }, []);

  return null; // blank screen while exchanging token
}
```

**`src/pages/Login.jsx`:**

```jsx
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { getKindeClient } from '../auth/kinde';
import { getToken } from '../auth/storage';
import { DEV_MODE } from '../config';
import LoginScreen from '../screens/LoginScreen';

export default function Login() {
  const navigate = useNavigate();
  const [signingIn, setSigningIn] = useState(false);

  useEffect(() => {
    if (DEV_MODE || getToken()) navigate('/', { replace: true });
  }, []);

  async function handleLogin() {
    setSigningIn(true);
    try {
      const client = await getKindeClient();
      await client.login(); // browser redirects; the Promise never resolves
    } catch (e) {
      console.error('Login failed:', e);
      setSigningIn(false);
    }
  }

  return <LoginScreen onLogin={handleLogin} loading={signingIn} />;
}
```

**`src/pages/Dashboard.jsx`:**

```jsx
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Navigate } from 'react-router-dom';
import DashboardScreen from '../screens/DashboardScreen';
import { fetchAccounts, setAuthToken } from '../api/client';
import { clearToken, getToken } from '../auth/storage';
import { queryClient } from '../main';

function parseJwt(token) {
  try { return JSON.parse(atob(token.split('.')[1].replace(/-/g,'+').replace(/_/g,'/'))); }
  catch { return {}; }
}

export default function Dashboard() {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const selectedAccount = params.get('account');

  const accounts = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });
  const claims   = parseJwt(getToken() ?? '');
  const orgName  = claims.org_name || claims.org_code || '';

  if (accounts.data?.length === 0) return <Navigate to="/connect" replace />;

  return (
    <DashboardScreen
      orgName={orgName}
      accounts={accounts.data ?? []}
      selectedAccount={selectedAccount}
      onSelectAccount={(id) => id ? setParams({ account: id }) : setParams({})}
      onSelectGhost={(g) =>
        navigate(`/detail/${g.resource_id}?account=${g.internal_account_id}&region=${g.region}&service=${g.service}`)
      }
      onShowTrend={() => navigate('/trend')}
      onConnectAccount={() => navigate('/connect')}
      onEditAccount={(acc) => navigate(`/settings/${acc.id}`)}
      onDeleteAccount={() => queryClient.invalidateQueries({ queryKey: ['accounts'] })}
      onLogout={async () => {
        clearToken(); setAuthToken(null); queryClient.clear();
        navigate('/login', { replace: true });
      }}
    />
  );
}
```

**`src/pages/Detail.jsx`:**

```jsx
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchResources } from '../api/client';
import { queryClient } from '../main';
import DetailScreen from '../screens/DetailScreen';
import { Spinner } from '../components/primitives';
import { useTheme } from '../theme/ThemeContext';

export default function Detail() {
  const navigate = useNavigate();
  const { id } = useParams();
  const [params] = useSearchParams();
  const { theme } = useTheme();
  const account = params.get('account');
  const region  = params.get('region');
  const service = params.get('service');

  const { data: resources, isLoading } = useQuery({
    queryKey: ['resources', account],
    queryFn: () => fetchResources(account),
  });

  const ghost = resources?.find(
    (r) => r.resource_id === id && r.service === service && r.region === region
  );

  const goBack = () => navigate(-1) || navigate('/');

  if (isLoading) return <div style={{ flex: 1, display: 'flex', justifyContent: 'center', alignItems: 'center', backgroundColor: theme.bg }}><Spinner /></div>;
  if (!ghost) return <NotFound />; // import from ./NotFound; avoids depending on a specific /404 path

  return (
    <DetailScreen
      ghost={ghost}
      onBack={goBack}
      onDismissed={() => {
        queryClient.invalidateQueries({ queryKey: ['resources'] });
        queryClient.invalidateQueries({ queryKey: ['ghosts'] });
      }}
    />
  );
}
```

**`src/pages/Trend.jsx`**, **`src/pages/Connect.jsx`**, **`src/pages/Settings.jsx`**,
**`src/pages/NotFound.jsx`** — follow the same wrapper pattern as the expo-router
equivalents already written in `app/(auth)/`. The only difference is `useNavigate()`
instead of `useRouter()` and `navigate(-1)` instead of `router.back()`.

---

### Task 6 — Convert screens

Convert each screen file in order of complexity. For each file the procedure is the
same:

1. Replace all React Native imports with HTML elements or the primitives from Task 2.
2. Remove `StyleSheet.create()` wrapper — keep the style object values unchanged.
3. Replace `onPress` → `onClick`, `onChangeText` → `onChange={e => f(e.target.value)}`.
4. Replace `Alert.alert(…)` → `window.confirm(…)` (or a custom modal if the UX
   warrants it).
5. Replace `FlatList` → `.map()` inside a `<div>`.
6. Replace `Modal` → `<Overlay visible={…}>` from Task 2.
7. Replace `RefreshControl` → remove; React Query's `refetch()` on a button is enough.
8. Replace `useWindowDimensions()` → `useWindowWidth()` from Task 2.

**Order:**

| Screen | RN APIs | Effort |
|---|---|---|
| `LoginScreen` | View, Text, TouchableOpacity, ActivityIndicator | Trivial |
| `ConnectScreen` | View, Text, TextInput, TouchableOpacity, ScrollView | Trivial |
| `AccountSettingsScreen` | + Modal, delete confirmation | Small |
| `AccountSelector` | + FlatList, Modal dropdown | Small |
| `DetailScreen` | + Modal, TextInput multiline, radio buttons | Medium |
| `TrendScreen` | + FlatList, ScrollView ref, useWindowDimensions | Medium |
| `DashboardScreen` | + FlatList, RefreshControl, Alert, sparkline | Medium |

Convert one screen, run the app, verify visually before moving to the next.

---

### Task 7 — Dockerfile and nginx

**`Dockerfile`** — swap the build command and env var prefix:

```dockerfile
FROM node:22-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .

ARG VITE_KINDE_ISSUER
ARG VITE_KINDE_CLIENT_ID
ARG VITE_API_URL=/api
ARG VITE_DEV_MODE=false
ENV VITE_KINDE_ISSUER=$VITE_KINDE_ISSUER
ENV VITE_KINDE_CLIENT_ID=$VITE_KINDE_CLIENT_ID
ENV VITE_API_URL=$VITE_API_URL
ENV VITE_DEV_MODE=$VITE_DEV_MODE

RUN npm run build         # vite build → dist/

FROM nginx:1.27-alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY inject-env.sh /docker-entrypoint.d/40-inject-env.sh
RUN chmod +x /docker-entrypoint.d/40-inject-env.sh
EXPOSE 80
```

**`nginx.conf`** — unchanged. SPA fallback and `/api` proxy are already correct.

**`inject-env.sh`** — keep the script as-is; it writes `window.__ENV__` (runtime
values, not build-time). The build-arg rename (`EXPO_PUBLIC_*` → `VITE_*`) only
affects the Dockerfile `ARG`/`ENV` block above. The runtime injected vars still
use the same unprefixed names (`DEV_MODE`, `KINDE_ISSUER`, …).

**Static assets.** Copy the favicon from `dashboard/assets/favicon.png` to
`dashboard-v2/public/favicon.ico` (or `.png` — update the `<link>` tag in
`index.html`). `app.json`, splash, and app icons from the Expo project are
native-only and can be dropped.

---

### Task 8 — Cut over

1. Rename `services/dashboard` → `services/dashboard-expo` (keep as fallback).
2. Rename `services/dashboard-v2` → `services/dashboard`.
3. Update `docker-compose.yml` — the build context (`./services/dashboard`) and
   `container_name` still resolve, but the build args must be renamed from
   `EXPO_PUBLIC_*` to `VITE_*`. Grep for any remaining `EXPO_PUBLIC_` references
   across the repo (`docker-compose.yml`, `Makefile`, `.env.example`, and any
   `.github/workflows/*.yml` or deployment scripts) — all must flip at the same
   moment or the staging build silently ships without its env vars.
4. Run `make start-dev`, verify the full auth + dashboard flow end-to-end.
5. Delete `services/dashboard-expo` once confident.

---

## Checklist

- [ ] Task 1: Vite scaffold, copy files, rewrite `config.js` for `import.meta.env`
- [ ] Task 2: Primitives (View, Text, Pressable, Spinner, Overlay, useWindowWidth) + `flatStyle` helper
- [ ] Task 3: Auth — `getKindeClient()` async singleton, simplified storage
- [ ] Task 4: Root layout and route table (App.jsx, AuthGuard, main.jsx)
- [ ] Task 5: Page files (Login, Callback, Dashboard, Detail, Trend, Connect, Settings, NotFound)
- [ ] Task 6: Screen conversions (LoginScreen → DashboardScreen, in order)
- [ ] Task 7: Dockerfile build-arg rename + favicon
- [ ] Task 8: Repo-wide `EXPO_PUBLIC_*` → `VITE_*` audit, cut over, smoke test, delete expo project

## Estimated effort

| Task | Effort |
|---|---|
| 1 — Scaffold | 2 h |
| 2 — Primitives | 2 h |
| 3 — Auth | 3 h |
| 4 — Layout + router | 2 h |
| 5 — Page files | 2 h |
| 6 — Screen conversions | 14 h |
| 7 — Docker + env | 1 h |
| 8 — Cut over + smoke test | 2 h |
| **Total** | **~28 h** |

Screen-conversion hours are padded from the original 8h to cover the
spot-the-difference cleanup pass per screen (flex defaults, inline `<Text>`
alignment, `<button>` text centering, and any style-array collapses the
primitives don't absorb).

## Out of scope

- Switching to Tailwind or CSS Modules (separate follow-up — no visual impact, do it
  when a screen needs a redesign anyway)
- Native mobile (decision deferred; React Router + Vite is web-only)
- Adding a test suite (dashboard has none today — separate initiative)
- **Token refresh.** The Kinde SDK's `getToken()` auto-refreshes, but
  `api/client.js` caches the access token via `setAuthToken` and won't pick up a
  fresh one — long sessions will 401 silently. This is a pre-existing bug, not
  migration-specific. Fix by calling `client.getToken()` on every request (or on
  a 401 retry) in a follow-up.
