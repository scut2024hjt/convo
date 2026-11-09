-- create the databases
CREATE DATABASE IF NOT EXISTS convo;
USE convo;
-- create table
DROP TABLE IF EXISTS `user`;
CREATE TABLE `user` (
                        `id` bigint(20) NOT NULL AUTO_INCREMENT,
                        `user_id` bigint(20) NOT NULL,
                        `username` varchar(64) COLLATE utf8_general_ci NOT NULL,
                        `password` varchar(64) COLLATE utf8_general_ci NOT NULL,
                        `email` varchar(64) COLLATE utf8_general_ci,
                        `gender` varchar(4) NOT NULL DEFAULT '0',
                        `create_time` timestamp NULL DEFAULT CURRENT_TIMESTAMP,
                        `update_time` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
                        PRIMARY KEY (`id`),
                        UNIQUE KEY `idx_username` (`username`) USING BTREE,
                        UNIQUE KEY `idx_user_id` (`user_id`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- create table
DROP TABLE IF EXISTS `community`;
CREATE TABLE `community` (
                             `id` int(11) NOT NULL AUTO_INCREMENT,
                             `community_id` int(10) unsigned NOT NULL,
                             `community_name` varchar(128) COLLATE utf8mb4_general_ci NOT NULL,
                             `introduction` varchar(256) COLLATE utf8mb4_general_ci NOT NULL,
                             `create_time` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
                             `update_time` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
                             PRIMARY KEY (`id`),
                             UNIQUE KEY `idx_community_id` (`community_id`),
                             UNIQUE key `idx_community_name` (`community_name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- insert data
INSERT INTO `community` VALUES ('1', '1', 'Go', 'Golang', '2016-11-01 08:10:10', '2016-11-01 08:10:10');
INSERT INTO `community` VALUES ('2', '2', 'leetcode', '刷题刷题刷题', '2020-01-01 08:00:00', '2020-01-01 08:00:00');
INSERT INTO `community` VALUES ('3', '3', 'CS:GO', 'Rush B 。。。', '2018-08-07 08:30:00', '2018-08-07 08:30:00');
INSERT INTO `community` VALUES ('4', '4', 'LOL', '欢迎来到英雄联盟!', '2016-01-01 08:00:00', '2018-8-07 08:30:00');

-- create table
DROP TABLE IF EXISTS `post`;
CREATE TABLE `post` (
                        `id` bigint(20) NOT NULL AUTO_INCREMENT,
                        `post_id` bigint(20) NOT NULL COMMENT '帖子id',
                        `title` varchar(128) COLLATE utf8mb4_general_ci NOT NULL COMMENT '标题',
                        `content` varchar(8192) COLLATE utf8mb4_general_ci NOT NULL COMMENT '内容',
                        `author_id` bigint(20) NOT NULL COMMENT '作者的用户id',
                        `community_id` bigint(20) NOT NULL COMMENT '所属社区',
                        `status` tinyint(4) NOT NULL DEFAULT '1' COMMENT '帖子状态',
                        `create_time` timestamp NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
                        `update_time` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
                        PRIMARY KEY (`id`),
                        UNIQUE KEY `idx_post_id` (`post_id`),
                        KEY `idx_author_id` (`author_id`),
                        KEY `idx_community_id` (`community_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

DROP TABLE IF EXISTS `post_vote`;
CREATE TABLE `post_vote` (
                         `user_id` bigint(20) NOT NULL COMMENT '投票用户id',
                         `post_id` bigint(20) NOT NULL COMMENT '帖子id',
                         `direction` tinyint(4) NOT NULL COMMENT '0取消点赞，1已点赞',
                         `version` bigint(20) unsigned NOT NULL COMMENT '同一用户对同一帖子的事件版本',
                         `last_event_id` varchar(64) NOT NULL COMMENT '最后应用的事件id',
                         `updated_at` timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
                         PRIMARY KEY (`user_id`, `post_id`),
                         KEY `idx_post_direction` (`post_id`, `direction`),
                         CONSTRAINT `chk_post_vote_direction` CHECK (`direction` IN (0, 1)),
                         CONSTRAINT `chk_post_vote_version` CHECK (`version` > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

DROP TABLE IF EXISTS `mq_consumed_message`;
CREATE TABLE `mq_consumed_message` (
                                  `event_id` varchar(64) NOT NULL,
                                  `event_type` varchar(64) NOT NULL,
                                  `consumed_at` timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
                                  PRIMARY KEY (`event_id`),
                                  KEY `idx_consumed_at` (`consumed_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Phase 1: new, disposable databases only. Existing DBs use migrations/002_follow_feed.sql.
CREATE TABLE user_follow (
  follower_id BIGINT NOT NULL,
  followee_id BIGINT NOT NULL,
  create_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (follower_id, followee_id),
  KEY idx_followee (followee_id, follower_id),
  CONSTRAINT fk_follow_follower FOREIGN KEY (follower_id) REFERENCES user(user_id),
  CONSTRAINT fk_follow_followee FOREIGN KEY (followee_id) REFERENCES user(user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
CREATE INDEX idx_feed_scan ON post (status, create_time DESC, post_id DESC, author_id);
CREATE INDEX idx_feed_author ON post (author_id, status, create_time DESC, post_id DESC);
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

-- T02 schema for disposable fresh databases; existing databases use migration 004.
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

-- T03 fresh databases start with v1 like semantics already complete (no legacy votes).
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
INSERT INTO like_semantics_state(id,phase) VALUES(1,'ready');
