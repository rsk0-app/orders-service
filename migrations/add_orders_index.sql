-- migration: add_orders_index
ALTER TABLE orders ADD COLUMN IF NOT EXISTS stand_marker text;
