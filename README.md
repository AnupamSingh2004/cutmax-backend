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
internal/storage           Storage interface — local disk or S3/R2, picked by STORAGE_DRIVER
internal/handlers         one file per route group (public, customer auth, enquiries, admin auth,
                           admin products/tiers/enquiries/settings/stats/audit, bulk import, uploads,
                           media library)
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
They run against the local storage driver regardless of what `STORAGE_DRIVER` is set to in your
shell — the integration test harness always forces `local` so tests never need real R2 credentials.

## Object storage (product images, media library)

Uploads — product photos and the standalone media library (`/admin/media`, for anything not tied to
a product: hero video, background video, etc.) — go through `internal/storage`, which is either:

- **`local`** (default): written to `UPLOADS_DIR`, served back out by this server at
  `UPLOADS_PUBLIC_BASE_URL` + key. Zero config, what you get out of the box.
- **`s3`**: an S3-compatible bucket (built against Cloudflare R2, but any S3-compatible provider
  works — it's just a custom endpoint). Set `STORAGE_DRIVER=s3` plus `S3_ENDPOINT`, `S3_REGION`,
  `S3_BUCKET`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, `S3_PUBLIC_BASE_URL` — see `.env.example`
  for where to get each of these from an R2 dashboard. `LoadConfig()` validates all five are present
  before the server will start with `STORAGE_DRIVER=s3`, so a half-configured setup fails at boot,
  not on the first upload.

The media library's DB table isn't created by anything automatic yet (this repo has no migration
runner) — apply it once by hand:

```bash
docker exec -i cutmax-backend-postgres-1 psql -U cutmax -d cutmax < migrations/0001_media_assets.sql
```

The homepage's hero video and sitewide background video are sourced from the `settings` table
(`hero_video_url` / `site_background_video_url`, editable from `/admin/settings` — paste in a URL
copied from `/admin/media`) and fall back to the bundled `cutmax-frontend/public/videos/*.mp4` files
when unset, so this is fully opt-in.

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
- File uploads are sniffed by content (magic bytes via `h2non/filetype`, not trusted by
  extension/filename) before being stored — product images accept jpeg/png/webp/gif only; the media
  library additionally accepts mp4/webm/mov.
- `S3_ACCESS_KEY_ID`/`S3_SECRET_ACCESS_KEY` are as sensitive as the JWT secrets — same rule applies:
  never commit them, only in `.env`. Scope the R2 API token to just the one bucket, not full account
  access.

## Deployment

```bash
docker build -t cutmax-backend .
docker run -p 3000:3000 --env-file .env cutmax-backend
```

The `Dockerfile` is a multi-stage build (`golang:1.24-alpine` → `alpine:3.19`), produces a static
binary, and never bakes `.env` or source `.go` files into the runtime image (see `.dockerignore`).
In production, run behind a reverse proxy that terminates TLS, set `NODE_ENV=production`, and point
`ALLOWED_ORIGINS`/`UPLOADS_PUBLIC_BASE_URL` at the real deployed domains. Set `STORAGE_DRIVER=s3` so
uploaded files live in R2 instead of the container's local disk — the local driver's files don't
survive a redeploy/restart on most hosting. `docker-compose.yml` as
checked in is for **local development only** (hardcoded Postgres password) — don't run it in
production; provision a managed Postgres/Redis instead and pass `DATABASE_URL`/`REDIS_URL` via the
real environment.
