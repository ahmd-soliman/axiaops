# expo-router Migration Plan

## Problem Statement

The dashboard currently uses in-memory state (`useState`) for navigation, preventing users from bookmarking specific resources or sharing deep links. URLs never change, making the app feel less like a web application.

## Requirements

- Enable bookmarkable URLs (e.g., `/detail/i-1234abcd`)
- Support shareable links to specific resources
- Maintain current UI/UX improvements
- Prepare for potential native mobile support
- **Don't change the API** — the backend has no `GET /v1/resources/:id` endpoint and we won't add one in this migration

## Background

### Current Architecture

- **Entry:** `App.js` → `registerRootComponent(App)` via `index.js`
- **Navigation:** `useState` hooks in `AuthenticatedApp` manage screen visibility (`selectedGhost`, `showConnect`, `showTrend`, `showAccountSettings`, `editAccount`, `selectedAccount`)
- **Screens:** 6 main screens (Login, Dashboard, Detail, Trend, Connect, AccountSettings)
- **Auth:** Kinde OAuth PKCE with token in `expo-secure-store` (native) / `localStorage` (web), plus a `DEV_MODE` shortcut that mints a fake JWT
- **State:** Prop drilling for navigation callbacks (`onBack`, `onSelectGhost`, `onConnectAccount`, etc.)
- **Providers:** `QueryClientProvider` and `ThemeProvider` in `App.js`; `ThemedApp` wraps everything with a theme-aware `SafeAreaView` + `StatusBar`
- **Remount trick:** `<AuthenticatedApp key={token}>` forces a full tree remount when the token changes

### Pre-migration Audit (April 2026)

Items **already done** (do not redo):

- `app.json` already declares `"scheme": "axiaops"` — Task 1's deep-linking config is partially complete
- `nginx.conf` already does SPA fallback (`try_files $uri $uri/ /index.html`) — no server config changes needed

Items that are **dead code** (do not propagate):

- `editAccount` state in `App.js` (lines 34, 62–72) is declared but `setEditAccount` is never called anywhere in `src/`. The `<ConnectScreen>` edit-mode branch (`account={editAccount}`) is unreachable. **Do not migrate `/connect?edit=acc_123` unless a UI trigger is added** (e.g., a "Change credentials" button in `AccountSettingsScreen`). If that trigger isn't in scope, drop the edit-mode route from this migration.

### Key Migration Challenges

1. **Auth flow.** Need a `_layout.js` guard that redirects unauthenticated users to `/login`, plus a path that handles the `DEV_MODE` auto-login (which currently runs in `Root`, not in a login screen).
2. **Detail-route identity.** `DetailScreen` receives the whole `ghost` object today. The composite key is `(internal_account_id, provider, service, region, resource_id)` — `resource_id` alone isn't unique. Since we can't add an API endpoint, the detail screen must reconstruct the object from the cached `resources` list (see Task 6 for the exact strategy).
3. **Query params.** Account filter (`selectedAccount`) and the zero-account Connect-screen auto-open need to be expressed via URL state.
4. **Cold-entry back navigation.** Bookmarked URLs have no history, so every `router.back()` must fall back to `router.replace('/')` when `!router.canGoBack()`.
5. **Zero-account redirect.** Current UX auto-opens `ConnectScreen` when `accounts.data.length === 0`. In the new flow, the dashboard route should emit `<Redirect href="/connect">` under the same condition.
6. **Theme wrapping.** `ThemedApp`'s theme-aware `SafeAreaView` + `StatusBar` currently wraps the whole app. Move this into `app/_layout.js` so it applies to both authenticated and login routes.
7. **Modal on web.** Expo Router's `presentation: 'modal'` is a native-only concept; on web it renders as a regular route. Current UX uses full-screen `ConnectScreen` / `AccountSettingsScreen` anyway, so this is acceptable.
8. **`key={token}` remount.** The current remount trick is replaced naturally by the auth guard: when `clearToken()` + `router.replace('/login')` runs, the `(auth)` layout unmounts and `QueryClient` cache can be cleared explicitly.

## Proposed Route Structure

```
app/
├── _layout.js              # Root layout: QueryClient, Theme, SafeAreaView, StatusBar, DEV_MODE bootstrap
├── +not-found.js           # Top-level 404 (unauthenticated-safe)
├── login.js                # Login screen (/login)
├── (auth)/
│   ├── _layout.js          # Auth guard (Redirect to /login if no token)
│   ├── index.js            # Dashboard (/)
│   ├── detail/[id].js      # Detail screen (/detail/<resource_id>?account=…&region=…&service=…)
│   ├── trend.js            # Trend screen (/trend)
│   ├── connect.js          # Connect screen (/connect)  — first-account + add-account
│   └── settings/[accountId].js  # Account settings (/settings/acc_123)
```

Note: `connect.js` and `settings/[accountId].js` live inside `(auth)/` because both require a token. They will look like full-screen routes on web regardless of any `presentation: 'modal'` option (see Challenge 7).

### URL Examples

| URL | Screen |
|-----|--------|
| `/` | Dashboard (all accounts) |
| `/?account=acc_123` | Dashboard filtered to one account |
| `/detail/i-1234abcd?account=acc_123&region=eu-central-1&service=AmazonEC2` | Resource detail view (composite-key qs) |
| `/trend` | Savings trend screen |
| `/login` | Login screen |
| `/connect` | Connect new AWS account |
| `/settings/acc_123` | Account settings (interval, name, delete) |

**Why the extra query params on `/detail`?** `resource_id` alone isn't globally unique, and there's no API endpoint to fetch a single resource by ID. The detail route uses the query params to locate the ghost in the cached `['resources', accountId]` list (see Task 6).

## Task Breakdown

### Task 1: Install expo-router and configure project

**Implementation:**
- `npx expo install expo-router react-native-safe-area-context react-native-screens expo-linking expo-constants`
  - `expo-status-bar` is already installed
  - Verify the installed `expo-router` version matches Expo SDK 54 (Expo Router ~6.x at the time of writing)
- Update `package.json`: set `"main": "expo-router/entry"` (replaces `registerRootComponent` in `index.js`)
- Delete or repurpose `index.js` (no longer the entry)
- `app.json`: scheme already present; no change required here
- Add `babel.config.js` plugin entry for `expo-router` if not auto-added by `npx expo install`

**Test:** Run `npm run web`, verify Metro recognises `app/` directory and no errors in the console.

---

### Task 2: Create root layout with providers

**Implementation:**
- Create `app/_layout.js` containing, in order:
  - `<QueryClientProvider client={queryClient}>` (move `queryClient` singleton from `App.js`)
  - `<ThemeProvider>` wrapped around a `<ThemedShell>` component that renders:
    - Theme-aware `<SafeAreaView style={{ flex: 1, backgroundColor: theme.bg }}>`
    - Theme-aware `<StatusBar barStyle=… backgroundColor=…>`
    - `<Stack screenOptions={{ headerShown: false }}>` (or `<Slot>` if no stack chrome is needed)
- Add a `DEV_MODE` bootstrap `useEffect` at this level: when `DEV_MODE` is true and no token is set, mint the dev JWT (`dev.<base64(org)>.dev`) and persist it via `saveToken` + `setAuthToken` before children mount. This replaces the equivalent block in `Root`.

**Test:** Theme, React Query, and dev-mode bootstrap all survive navigation between routes.

---

### Task 3: Create login route

**Implementation:**
- Create `app/login.js`
- Move the Kinde PKCE flow (`useKindeAuth`, `exchangeCodeAsync`, `saveToken`, `setAuthToken`) out of `Root` and into this route
- On successful token exchange: `router.replace('/')`
- Export default function `Login()` that renders the existing `LoginScreen` component with `onLogin={handleLogin}` and `loading={signingIn}`
- Keep the signing-in state inside this component

**Test:** Real Kinde login completes and redirects to dashboard. Dev mode skips this screen entirely (handled in `_layout.js`).

---

### Task 4: Create auth layout guard

**Implementation:**
- Create `app/(auth)/_layout.js`
- Read token via `useEffect` + `getToken()` (async) into local state, plus a `loading` flag for the first tick
- While loading, render `null` (matches current behaviour)
- If no token after load, render `<Redirect href="/login" />`
- Otherwise render `<Stack screenOptions={{ headerShown: false }} />` (or `<Slot>`)
- This guard replaces the `key={token}` remount trick: logging out + `router.replace('/login')` unmounts the entire `(auth)` subtree automatically

**Test:** Unauthenticated users visiting `/`, `/detail/…`, `/trend`, `/connect`, `/settings/…` all redirect to `/login`.

---

### Task 5: Create dashboard route

**Implementation:**
- Create `app/(auth)/index.js`
- Inline the `AuthenticatedApp` glue that's still useful: fetch `accounts` via `useQuery`, parse JWT for `orgName`
- Read `account` query param via `useLocalSearchParams()`; write it back with `router.setParams({ account: id })` when the user changes the filter
- If `accounts.data?.length === 0`, emit `<Redirect href="/connect" />` (replaces current auto-open effect)
- Wire navigation: `onSelectGhost={(g) => router.push({ pathname: '/detail/[id]', params: { id: g.resource_id, account: g.internal_account_id, region: g.region, service: g.service } })}`, `onShowTrend={() => router.push('/trend')}`, `onConnectAccount={() => router.push('/connect')}`, `onEditAccount={(acc) => router.push(\`/settings/\${acc.id}\`)}`, `onLogout={handleLogout}`
- `handleLogout`: `clearToken()` + `setAuthToken(null)` + `queryClient.clear()` + `router.replace('/login')`

**Test:** Dashboard renders; account filter round-trips through the URL; zero-account state redirects to `/connect`.

---

### Task 6: Create detail route

**Implementation:**
- Create `app/(auth)/detail/[id].js`
- Read `id` plus `account`, `region`, `service` from `useLocalSearchParams()`
- Reconstruct the ghost object from the `['resources', account]` React Query cache:
  1. Call `useQuery({ queryKey: ['resources', account], queryFn: () => fetchResources(account) })` — this is usually a cache hit when navigating from the dashboard
  2. Find the matching entry by `(resource_id, service, region)` tuple
  3. If found, render `<DetailScreen ghost={found} …>`
  4. If the query is loading, render a spinner
  5. If loading finishes and no match is found, render `<NotFound />` (or `router.replace('/+not-found')`)
- Replace the `onBack` prop:
  ```
  const goBack = () => router.canGoBack() ? router.back() : router.replace('/');
  ```
- Replace the `onDismissed` parent callback with a direct `queryClient.invalidateQueries({ queryKey: ['ghosts'] })` inside `DetailScreen`'s dismiss/restore handlers (or keep the prop and wire it here)

**Test:** Navigate to `/detail/i-1234?account=acc_1&region=eu-central-1&service=AmazonEC2` directly (cold entry); the resources query runs, resource loads, URL is bookmarkable. Unknown resource IDs show 404.

---

### Task 7: Create trend route

**Implementation:**
- Create `app/(auth)/trend.js`
- Wrap existing `TrendScreen` component
- Pass `onBack={() => router.canGoBack() ? router.back() : router.replace('/')}`

**Test:** `/trend` renders the chart; back button returns to the dashboard (or replaces with `/` on cold entry).

---

### Task 8: Create connect route

**Implementation:**
- Create `app/(auth)/connect.js`
- Wrap existing `ConnectScreen`
- `onConnected={() => { queryClient.invalidateQueries(); router.replace('/'); }}`
- `onSkip` and `onCancel` both: `router.canGoBack() ? router.back() : router.replace('/')`
- **Do not** implement `/connect?edit=…` unless a UI trigger is added to `AccountSettingsScreen` in a separate task — see Pre-migration Audit
- Omit `presentation: 'modal'` or document it as native-only (no effect on web)

**Test:** `/connect` loads, saves, and returns to dashboard. Zero-account redirect path from Task 5 lands here cleanly.

---

### Task 9: Create account settings route

**Implementation:**
- Create `app/(auth)/settings/[accountId].js`
- Read `accountId` via `useLocalSearchParams()`
- Look up the account from the `['accounts']` React Query cache (fetch if missing)
- Wrap existing `AccountSettingsScreen`
- `onBack`: `router.canGoBack() ? router.back() : router.replace('/')`
- `onAccountUpdated` / `onAccountDeleted`: `queryClient.invalidateQueries({ queryKey: ['accounts'] })` + `router.replace('/')`

**Test:** `/settings/acc_123` loads, save/delete flow works, URL is bookmarkable.

---

### Task 10: Update all navigation calls in screens

**Implementation:**
- Screen components keep their callback props (`onBack`, `onSelectGhost`, `onConnectAccount`, `onEditAccount`, `onShowTrend`, `onLogout`, `onSelectAccount`, `onConnected`, `onSkip`, `onCancel`, `onAccountUpdated`, `onAccountDeleted`, `onDismissed`) — those are the clean abstraction layer. **The route files** are what wire those callbacks to `router.*` calls (already covered by Tasks 5–9)
- Alternative (optional cleanup, can defer): replace callbacks with `useRouter()` directly inside screens. Not recommended in this migration because it couples presentational components to navigation

**Test:** All existing navigation flows work from end-to-end.

---

### Task 11: Handle logout

**Implementation:**
- `handleLogout` in `app/(auth)/index.js` (and any other authenticated routes that expose logout): `clearToken()` + `setAuthToken(null)` + `queryClient.clear()` + `router.replace('/login')`
- `router.replace` (not `push`) prevents back-navigation into an unauthenticated state

**Test:** Logout redirects to login; browser back button doesn't return to an authenticated screen; no stale cached data leaks across sessions.

---

### Task 12: Add 404 pages

**Implementation:**
- Top-level `app/+not-found.js` — generic "page not found" for any unmatched route (works without auth)
- Optionally `app/(auth)/+not-found.js` — authenticated 404 with app chrome; only needed if we want the auth guard to apply

**Test:** Typing an invalid URL shows the friendly error page. Invalid resource IDs from Task 6 end up here.

---

### Task 13: Clean up App.js / index.js

**Implementation:**
- Delete `App.js` (its logic is now distributed across `app/_layout.js`, `app/login.js`, `app/(auth)/_layout.js`, and `app/(auth)/index.js`)
- Replace `index.js` contents: with `"main": "expo-router/entry"` in `package.json`, this file is no longer the entry. Either delete it or leave a one-line stub; do not keep the old `registerRootComponent(App)` call
- Remove `editAccount` state entirely (dead code — see Pre-migration Audit)
- Remove `selectedAccount` state from `App.js` (now a URL param)

**Test:** App boots, no references to removed state, no regressions.

---

### Task 14: Test deep linking and bookmarking

**Checklist:**
- [ ] Direct navigation to `/detail/i-1234?account=…&region=…&service=…` by typing URL
- [ ] Browser back/forward buttons work
- [ ] Bookmark a detail page and return to it after a full page reload
- [ ] Share URL, open in new tab while logged in
- [ ] Share URL, open in new tab while logged out → redirects to `/login`, then after login lands back on the target (note: this requires either a `returnTo` query param or accepting that login always lands on `/` — pick one and document)
- [ ] Account filter persists in URL on refresh
- [ ] `/connect` auto-redirect triggers on 0-account state and goes away after first connect
- [ ] DEV_MODE (`EXPO_PUBLIC_DEV_MODE=true`) bypasses `/login` entirely
- [ ] Invalid resource ID shows 404, not a blank screen
- [ ] Logout clears React Query cache (no stale data visible after re-login as a different org)

---

## Out of Scope

- Adding `GET /v1/resources/:id` or any other API endpoint
- Implementing `/connect?edit=acc_123` credentials-edit flow (requires a new UI trigger; track separately)
- Unit/integration test suite for navigation (dashboard has no test infra today — separate initiative)
- Native mobile deep-linking QA (web-first)

## Migration Checklist

- [ ] Task 1: Install expo-router and configure project
- [ ] Task 2: Create root layout with providers (including DEV_MODE bootstrap and theme-aware shell)
- [ ] Task 3: Create login route
- [ ] Task 4: Create auth layout guard
- [ ] Task 5: Create dashboard route (with account-query-param + zero-account redirect)
- [ ] Task 6: Create detail route with composite-key lookup from cache
- [ ] Task 7: Create trend route
- [ ] Task 8: Create connect route
- [ ] Task 9: Create account settings route
- [ ] Task 10: Update navigation calls throughout app
- [ ] Task 11: Handle logout + clear query cache
- [ ] Task 12: Add 404 page(s)
- [ ] Task 13: Clean up App.js / index.js, remove dead `editAccount` state
- [ ] Task 14: Test deep linking and bookmarking (including cold-entry, DEV_MODE, logged-out share)

## Success Criteria

- All screens accessible via URLs
- Bookmarking specific resources works (cold-entry via composite-key query params)
- Browser back/forward buttons work; cold-entry back falls back to `/` via `canGoBack()`
- Auth flow works with redirects; DEV_MODE continues to bypass login
- Zero-account state continues to auto-route to `/connect`
- Logout clears the React Query cache
- No regressions in existing functionality
- Code is cleaner (no prop drilling for navigation, no `key={token}` remount trick, no dead `editAccount` state)
- Ready for native mobile deep linking in Phase 4
