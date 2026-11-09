-- T03: apply ONCE to existing MySQL 8 database after 001-004, during maintenance.
-- This deliberately does not tighten post_vote CHECK before -1 rows are converted.
-- Run cmd/migrate-likes to convert/check batches and tighten the CHECK, then
-- cmd/rebuild-redis to publish the new marker. DDL auto-commits, not rollbackable.
CREATE TABLE like_semantics_state (
  id TINYINT UNSIGNED NOT NULL PRIMARY KEY,
  phase VARCHAR(16) NOT NULL DEFAULT 'pending',
  last_user_id BIGINT NOT NULL DEFAULT 0,
  last_post_id BIGINT NOT NULL DEFAULT 0,
  scanned_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
  converted_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  CONSTRAINT chk_like_migration_phase CHECK (phase IN ('pending','migrating','converted','ready'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
INSERT INTO like_semantics_state (id,phase) VALUES (1,'pending');
