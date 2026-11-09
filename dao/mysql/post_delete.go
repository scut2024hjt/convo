package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrDeleteForbidden = errors.New("only the post author may delete this post")

// DeletePostWithDelivery owns the transactional success boundary. No Redis IO
// occurs while a MySQL row lock is held. Repeated owner DELETE is idempotent.
func DeletePostWithDelivery(ctx context.Context, postID, actorID, eventID int64) (bool, error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var p struct {
		AuthorID    int64     `db:"author_id"`
		CommunityID int64     `db:"community_id"`
		Status      int       `db:"status"`
		Created     time.Time `db:"create_time"`
	}
	err = tx.GetContext(ctx, &p, `SELECT author_id,community_id,status,create_time FROM post WHERE post_id=? FOR UPDATE`, postID)
	if err != nil {
		return false, err
	}
	if p.AuthorID != actorID {
		return false, ErrDeleteForbidden
	}
	if p.Status != 1 {
		return false, tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE post SET status=0 WHERE post_id=?`, postID); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO content_outbox
    (event_id,post_id,event_type,schema_version,author_id,community_id,occurred_at)
    VALUES (?,?,'post.deleted',1,?,?,UTC_TIMESTAMP())`, eventID, postID, p.AuthorID, p.CommunityID); err != nil {
		return false, err
	}
	for _, kind := range []string{ProjectionDeleteIndex, ProjectionDeleteInbox} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO content_projection_task(event_id,kind,status) VALUES (?,?,'pending')`, eventID, kind); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// PostVisible is the authoritative check for cache hits and new votes. Never
// assume a cached detail or Redis index proves that a post remains visible.
func PostVisible(ctx context.Context, postID int64) (bool, error) {
	var status int
	err := db.GetContext(ctx, &status, `SELECT status FROM post WHERE post_id=?`, postID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return status == 1, err
}

func UnfinishedDeletedPostIDs(ctx context.Context) ([]int64, error) {
	ids := []int64{}
	err := db.SelectContext(ctx, &ids, `SELECT e.post_id FROM content_projection_task t
    JOIN content_outbox e ON e.event_id=t.event_id JOIN post p ON p.post_id=e.post_id
    WHERE t.kind='delete_index' AND t.status IN ('pending','running','retry') AND p.status<>1`)
	return ids, err
}
