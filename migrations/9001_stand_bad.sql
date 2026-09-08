-- stand: intentionally broken migration (R4 real-failure mechanism).
-- Targets a table that does not exist, so the app's migration runner
-- throws on startup (Postgres 42P01 undefined_table) → migrate exits 1
-- → CrashLoopBackOff → ArgoCD Degraded. This is a REAL failure, not injected.
ALTER TABLE nonexistent_table_stand ADD COLUMN x int;
