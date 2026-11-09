-- Read-only operators' progress and EXPLAIN. Execute ONLY on selected T02 DB.
SELECT id,initialized,completed,upper_follower_id,upper_followee_id,
       last_follower_id,last_followee_id,updated_at FROM follow_bootstrap_state;
SELECT kind,status,COUNT(*) AS tasks,
       MIN(created_at) AS oldest_task,MAX(updated_at) AS last_update
FROM follow_delivery_task GROUP BY kind,status ORDER BY kind,status;
SELECT kind,status,COUNT(*) AS tasks FROM content_projection_task
GROUP BY kind,status ORDER BY kind,status;
SELECT task_id,follower_id,followee_id,generation,kind,status,
       attempts,consecutive_failures,upper_created_at,upper_post_id,
       last_created_at,last_post_id,last_error
FROM follow_delivery_task WHERE status IN ('retry','failed')
ORDER BY task_id LIMIT 100;
SELECT task_id,event_id,kind,status,attempts,consecutive_failures,
       last_follower_id,last_recipient_id,last_error
FROM content_projection_task WHERE status IN ('retry','failed')
ORDER BY task_id LIMIT 100;
EXPLAIN SELECT p.post_id,p.create_time FROM post p
WHERE p.author_id=1 AND p.status=1 ORDER BY p.create_time DESC,p.post_id DESC LIMIT 100;
EXPLAIN SELECT post_id FROM feed_inbox
WHERE recipient_id=1 AND author_id=2 AND follow_generation=1 AND post_id>0
ORDER BY post_id LIMIT 100;
EXPLAIN SELECT recipient_id FROM feed_inbox
WHERE post_id=1 AND recipient_id>0 ORDER BY recipient_id LIMIT 100;
