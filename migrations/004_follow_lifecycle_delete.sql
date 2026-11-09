-- T02: MySQL 8 incremental migration after 003. DDL auto-commits; back up first.
-- Preserve 001-003, posts, votes, content events and existing inbox rows.
ALTER TABLE user_follow
  ADD COLUMN active TINYINT(1) NOT NULL DEFAULT 1,
  ADD COLUMN generation BIGINT UNSIGNED NOT NULL DEFAULT 1,
  ADD CONSTRAINT chk_follow_active CHECK (active IN (0,1)),
  ADD CONSTRAINT chk_follow_generation CHECK (generation > 0);

ALTER TABLE feed_inbox
  ADD COLUMN author_id BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN follow_generation BIGINT UNSIGNED NOT NULL DEFAULT 0,
  ADD KEY idx_inbox_post (post_id, recipient_id),
  ADD KEY idx_inbox_relation (recipient_id, author_id, follow_generation, post_id);
-- Preserve legitimate T01 deliveries and bind them to the migrated active edge.
UPDATE feed_inbox i
JOIN post p ON p.post_id = i.post_id
JOIN user_follow f ON f.follower_id = i.recipient_id AND f.followee_id = p.author_id AND f.active = 1
SET i.author_id = p.author_id, i.follow_generation = f.generation;
-- Orphan and inactive references cannot be shown and are safe to discard.
DELETE FROM feed_inbox WHERE author_id = 0 OR follow_generation = 0;

ALTER TABLE content_projection_task
  ADD COLUMN last_recipient_id BIGINT NOT NULL DEFAULT 0,
  DROP CHECK chk_projection_kind,
  ADD CONSTRAINT chk_projection_kind CHECK (kind IN ('index','fanout','delete_index','delete_inbox'));

CREATE TABLE follow_delivery_task (
  task_id BIGINT NOT NULL AUTO_INCREMENT,
  follower_id BIGINT NOT NULL,
  followee_id BIGINT NOT NULL,
  generation BIGINT UNSIGNED NOT NULL,
  kind VARCHAR(12) NOT NULL,
  status VARCHAR(12) NOT NULL DEFAULT 'pending',
  attempts INT UNSIGNED NOT NULL DEFAULT 0,
  consecutive_failures INT UNSIGNED NOT NULL DEFAULT 0,
  next_run_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  lease_owner VARCHAR(128) NULL,
  lease_until DATETIME(6) NULL,
  claim_generation BIGINT UNSIGNED NOT NULL DEFAULT 0,
  upper_created_at TIMESTAMP(0) NULL,
  upper_post_id BIGINT NOT NULL DEFAULT 0,
  last_created_at TIMESTAMP(0) NULL,
  last_post_id BIGINT NOT NULL DEFAULT 0,
  last_error VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  completed_at DATETIME(6) NULL,
  PRIMARY KEY (task_id),
  UNIQUE KEY uq_follow_task (follower_id,followee_id,generation,kind),
  KEY idx_follow_task_due (kind,status,next_run_at,lease_until,task_id),
  CONSTRAINT chk_follow_task_kind CHECK (kind IN ('backfill','cleanup')),
  CONSTRAINT chk_follow_task_status CHECK (status IN ('pending','running','retry','done','failed','canceled'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE follow_bootstrap_state (
  id TINYINT NOT NULL PRIMARY KEY,
  upper_follower_id BIGINT NULL,
  upper_followee_id BIGINT NULL,
  last_follower_id BIGINT NOT NULL DEFAULT 0,
  last_followee_id BIGINT NOT NULL DEFAULT 0,
  initialized TINYINT(1) NOT NULL DEFAULT 0,
  completed TINYINT(1) NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  CONSTRAINT chk_bootstrap_id CHECK (id=1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
INSERT INTO follow_bootstrap_state(id) VALUES (1);
