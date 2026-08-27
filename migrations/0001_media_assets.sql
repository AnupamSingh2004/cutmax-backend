-- Standalone media library: images/video uploaded through the admin panel
-- that aren't tied to a specific product (e.g. the homepage hero video).
-- Product photos still live in products.image_url as before; this table is
-- for everything else.
--
-- Apply by hand for now (this repo has no migration runner yet):
--   docker exec -i cutmax-backend-postgres-1 psql -U cutmax -d cutmax < migrations/0001_media_assets.sql

CREATE TABLE IF NOT EXISTS media_assets (
    id text PRIMARY KEY,
    key text NOT NULL UNIQUE,
    url text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('IMAGE', 'VIDEO')),
    filename text NOT NULL,
    content_type text NOT NULL,
    size_bytes bigint NOT NULL,
    created_at timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS media_assets_created_at_idx ON media_assets (created_at DESC);
