# CLAUDE.md — Dashboard (Frontend)

## Purpose

React Native web dashboard for AxiaOps. Shows "The Ghost Number" (total idle spend),
ghost resource list, account management, and resource detail with remediation hints.

## Stack

- **React Native** via **Expo** (web-first, mobile in Phase 4)
- **React 19** + **TanStack Query** (data fetching/caching)
- **Kinde Auth** via `expo-auth-session` (PKCE flow)
- Served via **nginx** in production (static export)

## Key Screens

| Screen | File | Purpose |
|--------|------|---------|
| Dashboard | `src/screens/DashboardScreen.js` | Savings banner, ghost list, service filter pills, accounts bar |
| Detail | `src/screens/DetailScreen.js` | Resource details, stats grid, reason, remediation hint |
| Connect | `src/screens/ConnectScreen.js` | Credential form for connecting AWS accounts |

## API Client

`src/api/client.js` — fetch wrapper that:
- Sends `Authorization: Bearer <token>` on every request
- Points to `/api` (proxied through nginx) or direct URL via `EXPO_PUBLIC_API_URL`
- Handles token refresh via Kinde session

## Design System

- Dark navy background (`#0a1628`) with orange accent (`#f97316`) for savings numbers
- Per-service colour coding on ghost cards (EC2=blue, RDS=purple, Lambda=yellow, etc.)
- Status dots: green (connected), red (error), yellow (scanning) on account cards

## Environment Variables

| Variable | Required | Notes |
|----------|----------|-------|
| EXPO_PUBLIC_API_URL | No | Default: `/api` (nginx proxy) |
| EXPO_PUBLIC_KINDE_ISSUER | Prod | Kinde tenant URL |
| EXPO_PUBLIC_KINDE_CLIENT_ID | Prod | Kinde app ID |
| EXPO_PUBLIC_DEV_MODE | No | Skips auth, shows dev org name |

All `EXPO_PUBLIC_*` vars are baked into the static build at Docker image build time.
Changing them requires rebuilding the Docker image.

## Build & Run

```bash
cd services/dashboard
npm install
npm run web          # Local dev server (Expo)
npx expo export      # Static export for production
```

Docker build: `Dockerfile` runs `expo export` in Node stage, copies to nginx.

## Conventions

- Functional components with hooks (no class components)
- TanStack Query for all API calls — `useQuery` for reads, `useMutation` for writes
- No state management library — React Query cache + local component state
- Inline styles via React Native `StyleSheet.create()` (not CSS files)
- No TypeScript yet — plain JavaScript with JSDoc where helpful

## nginx Configuration

`nginx.conf` at service root:
- Serves Expo static build on port 80
- Proxies `/api/*` to the API service at `api:8080`
- Eliminates CORS issues in production (same-origin)
