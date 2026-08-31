-- Free-form spec sheet per product (e.g. Coating, Size Range, Shank Diameter)
-- shown on the product detail page alongside Category/Sub-Category/Material,
-- which already exist as real columns. Stored as a JSON array of
-- {"label": "...", "value": "..."} objects; NULL/empty means nothing to show.
--
-- Apply by hand for now (this repo has no migration runner yet):
--   docker exec -i cutmax-backend-postgres-1 psql -U cutmax -d cutmax < migrations/0004_product_specifications.sql

ALTER TABLE products ADD COLUMN IF NOT EXISTS specifications jsonb;
