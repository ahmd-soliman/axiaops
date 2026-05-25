# Vendored TLS trust anchors

## `rds-global-bundle.pem` — AWS RDS global CA bundle

Trust anchor for connecting to the production RDS instance with
`sslmode=verify-full`. RDS presents an Amazon-issued server certificate that is
**not** in the Mozilla CA bundle (`ca-certificates`), so `verify-full` needs
this explicit root.

### Where it's used

The production `DATABASE_URL` / `MIGRATION_DATABASE_URL` secrets (owned by the
`aws-infra` repo, stored in AWS Secrets Manager) connect with:

```
sslmode=verify-full&sslrootcert=/etc/ssl/certs/rds-ca-eu-central-1.pem
```

The `migrate`, `api`, and `ingestion` Dockerfiles `COPY` this file to that exact
path. Note the path is region-*named* but the contents are the **global**
bundle — see below.

`pgxpool.ParseConfig` loads `sslrootcert` at parse time, so a missing file fails
`Bootstrap` before any connection is even attempted (the original symptom:
`migrate` crash-looping with `unable to read CA file`).

Dev/staging (self-hosted) deploys use `sslmode=disable` and never read this file.

### Why the global bundle (not `eu-central-1-bundle.pem`)

AWS rotates RDS CAs periodically (e.g. `rds-ca-2019` → `rds-ca-rsa2048-g1`). The
global bundle contains every regional root **plus** the forward-dated rotation
CAs, so `verify-full` keeps working across a rotation without a redeploy. A
region-specific bundle would break on a CA change.

### Source & refresh

```bash
curl -fsSL https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem \
  -o deploy/certs/rds-global-bundle.pem
```

Official reference:
https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/UsingWithRDS.SSL.html

**Refresh cadence:** rarely needed — the global bundle is forward-dated by years.
Refresh *ahead* of a published AWS CA expiry, not reactively. After refreshing,
verify the bundle parses and rebuild the images:

```bash
openssl crl2pkcs7 -nocrl -certfile deploy/certs/rds-global-bundle.pem | \
  openssl pkcs7 -print_certs -noout | grep -c subject   # sanity: cert count
```
