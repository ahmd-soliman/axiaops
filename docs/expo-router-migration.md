# expo-router Migration Plan

## Problem Statement

The dashboard currently uses in-memory state (`useState`) for navigation, preventing users from bookmarking specific resources or sharing deep links. URLs never change, making the app feel less like a web application.

## Requirements

- Enable bookmarkable URLs (e.g., `/detail/i-1234abcd`)
- Support shareable links to specific resources
- Maintain current UI/UX improvements
- Prepare for potential native mobile support
- Don't change the API

## Background

### Current Architecture

- **Entry:** `App.js` with nested `<AuthenticatedApp>` component
- **Navigation:** `useState` hooks manage screen visibility (`selectedGhost`, `showConnect`, `showTrend`, etc.)
- **Screens:** 6 main screens (Login, Dashboard, Detail, Trend, Connect, AccountSettings)
- **Auth:** Kinde OAuth with token storage
- **State:** Props drilling for navigation callbacks (`onBack`, `onSelectGhost`, etc.)

### Key Migration Challenges

1. Auth flow: Need `_layout.js` to guard routes
2. Query params: Resource IDs, account filters need URL encoding
3. Modal routes: Connect/Settings screens should be modals
4. State persistence: `selectedAccount` filter needs URL sync

## Proposed Route Structure

```
app/
├── _layout.js              # Root layout (QueryClient, Theme)
├── (auth)/
│   ├── _layout.js          # Auth guard + shared navbar
│   ├── index.js            # Dashboard (/)
│   ├── detail/[id].js      # Detail screen (/detail/i-1234)
│   ├── trend.js            # Trend screen (/trend)
│   └── +not-found.js       # 404 page
├── login.js                # Login screen (/login)
├── connect.js              # Connect modal (/connect?edit=acc_123)
└── settings/[accountId].js # Account settings modal
```

### URL Examples

| URL | Screen |
|-----|--------|
| `/` | Dashboard (all accounts) |
| `/?account=acc_123` | Dashboard filtered to one account |
| `/detail/i-1234abcd` | Resource detail view |
| `/trend` | Savings trend screen |
| `/login` | Login screen |
| `/connect` | Connect new AWS account |
| `/connect?edit=acc_123` | Edit existing account |
| `/settings/acc_123` | Account settings |

## Task Breakdown

### Task 1: Install expo-router and configure project

**Implementation:**
- `npx expo install expo-router react-native-safe-area-context react-native-screens expo-linking expo-constants expo-status-bar`
- Update `package.json`: Set `"main": "expo-router/entry"`
- Update `app.json`: Add `scheme` for deep linking

**Test:** Run `npm start`, verify Metro recognises `app/` directory.

---

### Task 2: Create root layout with providers

**Implementation:**
- Create `app/_layout.js`
- Move `QueryClientProvider`, `ThemeProvider` from `App.js`
- Add `<Stack>` navigator with `headerShown: false`

**Test:** Theme and React Query work across navigation.

---

### Task 3: Create login route

**Implementation:**
- Create `app/login.js`
- Move login logic from `Root` component
- Use `router.replace('/')` after successful login

**Test:** Login flow completes and redirects to dashboard.

---

### Task 4: Create auth layout guard

**Implementation:**
- Create `app/(auth)/_layout.js`
- Check for token with `getToken()` on mount
- Use `<Redirect href="/login">` if no token
- Render `<Slot>` for authenticated routes

**Test:** Unauthenticated users redirect to login.

---

### Task 5: Create dashboard route

**Implementation:**
- Create `app/(auth)/index.js`
- Move `DashboardScreen` component
- Read `account` query param with `useLocalSearchParams()`
- Replace `onSelectGhost` with `router.push(\`/detail/\${id}\`)`
- Replace `onShowTrend` with `router.push('/trend')`

**Test:** Dashboard renders, account filter persists in URL.

---

### Task 6: Create detail route

**Implementation:**
- Create `app/(auth)/detail/[id].js`
- Read `id` param with `useLocalSearchParams()`
- Use `router.back()` for back navigation

**Test:** Navigate to `/detail/i-1234`, resource loads. URL is bookmarkable.

---

### Task 7: Create trend route

**Implementation:**
- Create `app/(auth)/trend.js`
- Move `TrendScreen` component
- Use `router.back()` for back navigation

**Test:** Navigate to `/trend`, chart renders.

---

### Task 8: Create connect modal route

**Implementation:**
- Create `app/connect.js`
- Move `ConnectScreen` component
- Read `edit` query param for edit mode
- Configure as `presentation: 'modal'` in Stack.Screen

**Test:** `/connect` opens as modal, saves and closes.

---

### Task 9: Create account settings modal route

**Implementation:**
- Create `app/settings/[accountId].js`
- Move `AccountSettingsScreen` component
- Configure as `presentation: 'modal'`

**Test:** `/settings/acc_123` opens as modal, updates work.

---

### Task 10: Update all navigation calls

**Implementation:**
- Replace all `onBack` props with `router.back()`
- Replace all `onConnectAccount` with `router.push('/connect')`
- Replace all `onEditAccount` with `router.push(\`/settings/\${id}\`)`
- Remove all navigation callback props from component signatures

**Test:** All navigation flows work, no broken navigation.

---

### Task 11: Handle logout

**Implementation:**
- Clear token with `clearToken()`
- Use `router.replace('/login')` (replace prevents back navigation to authenticated screens)

**Test:** Logout redirects to login, back button doesn't return to dashboard.

---

### Task 12: Add 404 page

**Implementation:**
- Create `app/(auth)/+not-found.js`
- Handle invalid resource IDs in detail screen

**Test:** Invalid URLs show friendly error page.

---

### Task 13: Clean up App.js

**Implementation:**
- Remove `AuthenticatedApp` component
- Remove all `useState` navigation state
- Remove prop drilling for navigation callbacks

**Test:** App works without old navigation code, no regressions.

---

### Task 14: Test deep linking and bookmarking

**Checklist:**
- [ ] Direct navigation to `/detail/i-1234` by typing URL
- [ ] Browser back/forward buttons work
- [ ] Bookmark a detail page and return to it
- [ ] Share URL, open in new tab
- [ ] Account filter persists in URL on refresh
- [ ] Modal routes open and close correctly

---

## Migration Checklist

- [ ] Task 1: Install expo-router and configure project
- [ ] Task 2: Create root layout with providers
- [ ] Task 3: Create login route
- [ ] Task 4: Create auth layout guard
- [ ] Task 5: Create dashboard route
- [ ] Task 6: Create detail route with dynamic ID
- [ ] Task 7: Create trend route
- [ ] Task 8: Create connect modal route
- [ ] Task 9: Create account settings modal route
- [ ] Task 10: Update navigation calls throughout app
- [ ] Task 11: Handle logout and auth state
- [ ] Task 12: Add 404 page
- [ ] Task 13: Clean up App.js
- [ ] Task 14: Test deep linking and bookmarking

## Success Criteria

- All screens accessible via URLs
- Bookmarking specific resources works
- Browser back/forward buttons work
- Auth flow works with redirects
- No regressions in existing functionality
- Code is cleaner (no prop drilling)
- Ready for native mobile deep linking
