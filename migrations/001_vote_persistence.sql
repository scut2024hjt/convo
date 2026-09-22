CREATE TABLE IF NOT EXISTS `post_vote` (
  `user_id` bigint(20) NOT NULL COMMENT '投票用户id',
  `post_id` bigint(20) NOT NULL COMMENT '帖子id',
  `direction` tinyint(4) NOT NULL COMMENT '-1反对，0取消，1赞成',
  `version` bigint(20) unsigned NOT NULL COMMENT '同一用户对同一帖子的事件版本',
  `last_event_id` varchar(64) NOT NULL COMMENT '最后应用的事件id',
  `updated_at` timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`user_id`, `post_id`),
  KEY `idx_post_direction` (`post_id`, `direction`),
  CONSTRAINT `chk_post_vote_direction` CHECK (`direction` IN (-1, 0, 1)),
  CONSTRAINT `chk_post_vote_version` CHECK (`version` > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS `mq_consumed_message` (
  `event_id` varchar(64) NOT NULL,
  `event_type` varchar(64) NOT NULL,
  `consumed_at` timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`event_id`),
  KEY `idx_consumed_at` (`consumed_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
