-- Adds a material field to products (e.g. "Carbide", "HSS") so the storefront
-- can filter by it. Nullable — existing rows have no material until an admin
-- sets one.
--
-- Apply by hand for now (this repo has no migration runner yet):
--   docker exec -i cutmax-backend-postgres-1 psql -U cutmax -d cutmax < migrations/0002_product_material.sql

ALTER TABLE products ADD COLUMN IF NOT EXISTS material text;

CREATE INDEX IF NOT EXISTS products_material_idx ON products (material) WHERE material IS NOT NULL;
