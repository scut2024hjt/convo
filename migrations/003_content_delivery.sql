-- T01: Execute ONCE after 001/002 on existing MySQL 8 databases.
-- MySQL DDL auto-commits. See docs/T01_CONTENT_DELIVERY.md for preflight/retry steps.
CREATE TABLE content_outbox (
    event_id BIGINT NOT NULL,
    post_id BIGINT NOT NULL,
    event_type VARCHAR(40) NOT NULL,
    schema_version SMALLINT UNSIGNED NOT NULL,
    author_id BIGINT NOT NULL,
    community_id BIGINT NOT NULL,
    occurred_at TIMESTAMP(0) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (event_id),
    UNIQUE KEY uq_content_post_event (post_id, event_type),
    KEY idx_content_post (post_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE content_projection_task (
    task_id BIGINT NOT NULL AUTO_INCREMENT,
    event_id BIGINT NOT NULL,
    kind VARCHAR(12) NOT NULL,
    status VARCHAR(12) NOT NULL DEFAULT 'pending',
    attempts INT UNSIGNED NOT NULL DEFAULT 0,
    consecutive_failures INT UNSIGNED NOT NULL DEFAULT 0,
    next_run_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    lease_owner VARCHAR(128) NULL,
    lease_until DATETIME(6) NULL,
    generation BIGINT UNSIGNED NOT NULL DEFAULT 0,
    scan_upper_id BIGINT NULL,
    last_follower_id BIGINT NOT NULL DEFAULT 0,
    last_error VARCHAR(255) NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    completed_at DATETIME(6) NULL,
    PRIMARY KEY (task_id),
    UNIQUE KEY uq_projection_event_kind (event_id, kind),
    KEY idx_projection_due (kind, status, next_run_at, lease_until, task_id),
    KEY idx_projection_lease (kind, status, lease_until, task_id),
    CONSTRAINT fk_projection_event FOREIGN KEY (event_id) REFERENCES content_outbox (event_id),
    CONSTRAINT chk_projection_kind CHECK (kind IN ('index', 'fanout')),
    CONSTRAINT chk_projection_status CHECK (status IN ('pending', 'running', 'retry', 'done', 'failed'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE feed_inbox (
    recipient_id BIGINT NOT NULL,
    post_id BIGINT NOT NULL,
    create_time TIMESTAMP(0) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (recipient_id, post_id),
    KEY idx_inbox_feed (recipient_id, create_time DESC, post_id DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
