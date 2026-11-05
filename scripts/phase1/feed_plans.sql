-- Read-only EXPLAIN ANALYZE templates for MySQL 8.0.
-- Run on dedicated test DB. Replace reader and cursor with actual generated IDs.
-- Same dataset, same page size; compare elapsed time and actual rows.
USE convo;
SET @reader_id = 9007199254740993;
SET @cursor_ts = 1767268800;
SET @cursor_id = 7100000000000000000;

-- A. Offset deep page (comparison baseline):
EXPLAIN ANALYZE
SELECT p.post_id, p.author_id, u.username, p.title, p.create_time, c.community_name
FROM post p JOIN user_follow f ON f.followee_id = p.author_id AND f.follower_id = @reader_id
JOIN user u ON u.user_id = p.author_id
JOIN community c ON c.community_id = p.community_id
WHERE p.status = 1
ORDER BY p.create_time DESC, p.post_id DESC LIMIT 20 OFFSET 2000;

-- B. Keyset with optimizer's natural index choice:
EXPLAIN ANALYZE
SELECT p.post_id, p.author_id, u.username, p.title, p.create_time, c.community_name
FROM post p JOIN user_follow f ON f.followee_id = p.author_id AND f.follower_id = @reader_id
JOIN user u ON u.user_id = p.author_id
JOIN community c ON c.community_id = p.community_id
WHERE p.status = 1
  AND (p.create_time < FROM_UNIXTIME(@cursor_ts)
       OR (p.create_time = FROM_UNIXTIME(@cursor_ts) AND p.post_id < @cursor_id))
ORDER BY p.create_time DESC, p.post_id DESC LIMIT 20;

-- C/D. Index candidates; FORCING is for comparison, not a production hint.
EXPLAIN ANALYZE
SELECT p.post_id, p.author_id, u.username, p.title, p.create_time, c.community_name
FROM post p FORCE INDEX (idx_feed_scan)
JOIN user_follow f ON f.followee_id = p.author_id AND f.follower_id = @reader_id
JOIN user u ON u.user_id = p.author_id
JOIN community c ON c.community_id = p.community_id
WHERE p.status = 1
ORDER BY p.create_time DESC, p.post_id DESC LIMIT 20;

EXPLAIN ANALYZE
SELECT p.post_id, p.author_id, u.username, p.title, p.create_time, c.community_name
FROM post p FORCE INDEX (idx_feed_author)
JOIN user_follow f ON f.followee_id = p.author_id AND f.follower_id = @reader_id
JOIN user u ON u.user_id = p.author_id
JOIN community c ON c.community_id = p.community_id
WHERE p.status = 1
ORDER BY p.create_time DESC, p.post_id DESC LIMIT 20;
