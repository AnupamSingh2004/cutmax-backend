-- Per-product low-stock threshold, overriding the global "low_stock_limit"
-- setting for products that need a different cutoff (e.g. a fast-moving
-- item where 10 units left is already worth flagging, vs. a slow-moving
-- item where 10 is plenty). NULL means "use the global default".
ALTER TABLE products ADD COLUMN IF NOT EXISTS low_stock_threshold INTEGER;
