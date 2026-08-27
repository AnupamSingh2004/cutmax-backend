# CutMax Backend

Go API for CutMax Technologies — product catalogue, customer accounts, enquiry cart, and the admin
panel backing `cutmax-frontend`. Chi router, Postgres via pgx, Redis for rate limiting, JWT session
cookies (separate customer and admin auth).

## Structure

```
cmd/server/main.go     entrypoint — config, DB pool, router, graceful shutdown
internal/config         env-driven Config struct
internal/db              Postgres pool + query helpers, ProductRow/PriceTierRow
internal/middleware      security headers, CORS, CSRF (double-submit cookie), rate limiting,
                          JWT sign/verify, admin/customer session middleware
internal/util             JSON response helpers, enquiry reference generation, small utils
internal/handlers         one file per route group (public, customer auth, enquiries, admin auth,
                           admin products/tiers/enquiries/settings/stats/audit, bulk import, uploads)
internal/router           the route table — single source of truth, used by both cmd/server and
                           the integration tests
```

## Local development

Requires Go 1.25+, Docker (for Postgres/Redis/Mailhog).

```bash
docker compose up -d          # postgres, redis, mailhog
cp .env.example .env          # fill in real JWT secrets, see below
go run ./cmd/server
```

The API listens on `http://localhost:3000`. Run `cutmax-frontend` alongside it at
`http://localhost:3001` (its `NEXT_PUBLIC_API_URL` should point here).

### Generating JWT secrets

`CUSTOMER_JWT_SECRET` and `ADMIN_JWT_SECRET` must each be at least 32 random characters, and must
be **different from each other** (a customer token must never verify as an admin token):

```bash
openssl rand -base64 48
```

## Tests

```bash
docker compose up -d          # tests need real Postgres + Redis
go test -count=1 ./...
```

63 tests: unit tests per package (`internal/config`, `internal/middleware`, `internal/util`) plus
full HTTP integration tests in `internal/handlers` that exercise the real router against a real
database (register/login/enquiries/admin CRUD/rate limiting/CSRF/security headers/account lockout).

## Security notes

- Customer and admin sessions are separate JWTs in separate httpOnly cookies — an admin token
  cannot be replayed as a customer token or vice versa.
- Cookies are `Secure` + `SameSite=None` only when `NODE_ENV=production` (needed for a
  cross-subdomain frontend/backend split); `SameSite=Lax`, non-Secure in development so they work
  over plain HTTP on localhost.
- CSRF: double-submit cookie plus an `X-Requested-With` marker header, enforced on every mutating
  request; `ALLOWED_ORIGINS` is the source of truth for both CORS and CSRF origin checks.
- Login is rate-limited per IP+email via Redis, with account lockout after repeated failures.
- Passwords are bcrypt (cost 12); minimum password length is enforced server-side.
- `.env` is gitignored and must never be committed — see `.env.example` for the variables to set.
  Rotate `CUSTOMER_JWT_SECRET`/`ADMIN_JWT_SECRET` if they're ever exposed; that invalidates all
  existing sessions.
- File uploads are sniffed by content (not trusted by extension/filename) before being written
  under `UPLOADS_DIR`.

## Deployment

```bash
docker build -t cutmax-backend .
docker run -p 3000:3000 --env-file .env cutmax-backend
```

The `Dockerfile` is a multi-stage build (`golang:1.24-alpine` → `alpine:3.19`), produces a static
binary, and never bakes `.env` or source `.go` files into the runtime image (see `.dockerignore`).
In production, run behind a reverse proxy that terminates TLS, set `NODE_ENV=production`, and point
`ALLOWED_ORIGINS`/`UPLOADS_PUBLIC_BASE_URL` at the real deployed domains. `docker-compose.yml` as
checked in is for **local development only** (hardcoded Postgres password) — don't run it in
production; provision a managed Postgres/Redis instead and pass `DATABASE_URL`/`REDIS_URL` via the
real environment.
