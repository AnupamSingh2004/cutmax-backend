-- Per-product volume pricing. Replaces the idea of a single global discount
-- table (price_tiers, still present but no longer surfaced in the admin UI)
-- with explicit price-per-unit breakpoints scoped to one product. Optional:
-- a product with no rows here just sells at its normal price at any quantity.
--
-- Apply by hand for now (this repo has no migration runner yet):
--   docker exec -i cutmax-backend-postgres-1 psql -U cutmax -d cutmax < migrations/0003_product_price_breaks.sql

CREATE TABLE IF NOT EXISTS product_price_breaks (
    id text PRIMARY KEY,
    product_id text NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    min_qty integer NOT NULL CHECK (min_qty > 0),
    unit_price numeric(10,2) NOT NULL CHECK (unit_price >= 0),
    created_at timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (product_id, min_qty)
);

CREATE INDEX IF NOT EXISTS product_price_breaks_product_id_idx ON product_price_breaks (product_id);
