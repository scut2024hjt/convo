-- Run once on an existing database; DO NOT run init.sql against existing data.
CREATE TABLE user_follow (
  follower_id BIGINT NOT NULL,
  followee_id BIGINT NOT NULL,
  create_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (follower_id, followee_id),
  KEY idx_followee (followee_id, follower_id),
  CONSTRAINT fk_follow_follower FOREIGN KEY (follower_id) REFERENCES user(user_id),
  CONSTRAINT fk_follow_followee FOREIGN KEY (followee_id) REFERENCES user(user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Two candidate access paths: recent global posts, and posts by followed author.
CREATE INDEX idx_feed_scan ON post (status, create_time DESC, post_id DESC, author_id);
CREATE INDEX idx_feed_author ON post (author_id, status, create_time DESC, post_id DESC);
