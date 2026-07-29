-- baseline: orders-service owns the orders table (R2 realistic-stand).
CREATE TABLE IF NOT EXISTS orders (
  id         text PRIMARY KEY,
  item       text NOT NULL,
  qty        integer NOT NULL,
  status     text NOT NULL DEFAULT 'created',
  created_at timestamptz NOT NULL DEFAULT now()
);
