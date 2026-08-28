---
title: Testing
description: Comprehensive testing strategy for unit, storage, integration, E2E Playwright, and AWS verification.
---

AxiaOps uses a multi-layered testing strategy arranged from fastest/most isolated to slowest/most authoritative.

## 1. Unit Tests

Unit tests verify business logic, HTTP request handlers, analyzer rules, and provider interfaces in isolation with mocked dependencies and zero external network/database calls.

- **Scope**: `services/shared`, `services/api`, `services/ingestion`, `services/dashboard`
- **Execution**: Runs in parallel without containers.

```bash
# Run all Go unit tests
make test

# Run frontend React/Vite dashboard unit tests
make test-dashboard
```

---

## 2. Storage & Database Isolation Tests

Storage tests certify the PostgreSQL 17 database layer, Row-Level Security (RLS) tenant isolation policies, SQL migrations, and concurrent transaction safety.

- **Scope**: `services/shared/storage/postgres`
- **Mechanism**: Spins up a clean, isolated PostgreSQL 17 container, executes all SQL schema migrations, and runs tests with `TRUNCATE CASCADE` hooks.
- **Execution**: Runs serially (`-p=1`) to prevent database state collision.

```bash
make test-storage
```

---

## 3. Microservice Integration Tests

Integration tests verify the full multi-container pipeline (`ingestion` → `analyzer` → `postgres/redis` → `api`) and authentication flows in Docker Compose environments.

- **API Integration**: Tests HTTP endpoints, argon2id session cookies, and multi-tenant RLS scoping.
- **Ingestion Integration**: Tests discovery collectors, CloudWatch metric parsers, and zombie resource storage.
- **SSO / OIDC Integration**: Drives full OAuth2 Authorization Code + PKCE flows against an in-process OIDC provider.

```bash
# Run API integration suite
make test-integration-api

# Run Ingestion pipeline integration suite
make test-integration-ingestion

# Run SSO / OIDC authentication flow suite
make test-integration-sso
```

---

## 4. End-to-End (E2E) Browser Tests (Playwright)

The E2E suite verifies full user journeys in Playwright against real production container shapes (`DEV_MODE=false`, real cookie sessions, fresh database).

- **Structure**:
  - `setup`: Drives the initial organization bootstrap ceremony and saves authenticated browser storage states.
  - `flows`: Reuses saved sessions for parallelized user navigation, dashboard metric visualization, and zombie remediation specs.
  - `no-auth`: Exercises auth lifecycle actions (logout, invite-redeem, password reset).
- **Ground Rules**: Build preconditions via API/SQL seeds; use stable accessibility locators (`getByRole`/`getByLabel`); zero artificial `sleep` delays.

```bash
# Run Playwright E2E regression suite
make test-e2e
```

---

## 5. Real AWS Verification Testing

AxiaOps tests AWS discovery and zombie rules against a dedicated AWS test account—never inaccurate emulators.

- **Why Not Emulators**: Emulators diverge from AWS wire formats and miss cross-service ID validation. Real AWS response captures guarantee zero false positives.
- **Captured Golden Fixtures**: Real AWS API responses are saved to `services/ingestion/internal/provider/aws/testdata/` for deterministic offline replay in unit and integration tests.
