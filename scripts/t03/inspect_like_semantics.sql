-- READ-ONLY checks. Connect to an explicitly selected *_t03 database.
-- Select a target DB in the mysql client; do not source this on default convo.
SELECT DATABASE() AS target_db, VERSION() AS mysql_version;
SELECT phase,last_user_id,last_post_id,scanned_count,converted_count FROM like_semantics_state WHERE id=1;
SELECT direction,COUNT(*) AS pairs,MIN(version) AS min_version,MAX(version) AS max_version FROM post_vote GROUP BY direction ORDER BY direction;
SELECT COUNT(*) AS invalid_rows FROM post_vote WHERE direction NOT IN (0,1) OR version=0 OR version>9007199254740991;
SELECT COUNT(*) AS consumed_events FROM mq_consumed_message;
SELECT COUNT(*) AS visible_posts FROM post WHERE status=1;
SELECT COUNT(*) AS removed_posts FROM post WHERE status<>1;
SELECT COALESCE(SUM(direction=1),0) AS likes FROM post_vote;
SELECT COUNT(*) AS published_events FROM content_outbox WHERE event_type='post.published';
SELECT COUNT(*) AS deleted_events FROM content_outbox WHERE event_type='post.deleted';
