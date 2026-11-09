package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/scut2024hjt/convo/models"
)

var ErrProjectionLeaseLost = errors.New("content projection lease was lost")

const (
	ProjectionIndex       = "index"
	ProjectionFanout      = "fanout"
	ProjectionDeleteIndex = "delete_index"
	ProjectionDeleteInbox = "delete_inbox"
)

// ProjectionTask is a durable work item and immutable event snapshot. A lease
// generation fences an old process even when the same owner string is reused.
type ProjectionTask struct {
	TaskID          int64         `db:"task_id"`
	EventID         int64         `db:"event_id"`
	PostID          int64         `db:"post_id"`
	AuthorID        int64         `db:"author_id"`
	CommunityID     int64         `db:"community_id"`
	OccurredAt      time.Time     `db:"occurred_at"`
	Kind            string        `db:"kind"`
	Status          string        `db:"status"`
	Attempts        int           `db:"attempts"`
	FailureCount    int           `db:"consecutive_failures"`
	Generation      uint64        `db:"generation"`
	LeaseOwner      string        `db:"lease_owner"`
	LastFollowerID  int64         `db:"last_follower_id"`
	LastRecipientID int64         `db:"last_recipient_id"`
	ScanUpperID     sql.NullInt64 `db:"scan_upper_id"`
}

// CreatePostWithDelivery is the sole success boundary of an HTTP publish.
// When any INSERT fails, no post/event/projection row can be committed.
func CreatePostWithDelivery(ctx context.Context, p *models.Post, eventID int64) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Author locking read serializes publication and the beginning of a new
	// follow. Neither transaction holds this lock during background processing.
	var author int64
	if err = tx.GetContext(ctx, &author, `SELECT user_id FROM user WHERE user_id=? FOR UPDATE`, p.AuthorID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO post (post_id,title,content,author_id,community_id) VALUES (?,?,?,?,?)`,
		p.ID, p.Title, p.Content, p.AuthorID, p.CommunityID); err != nil {
		return err
	}
	if err = tx.GetContext(ctx, &p.CreateTime, `SELECT create_time FROM post WHERE post_id=?`, p.ID); err != nil {
		return err
	}
	p.CreateTime = p.CreateTime.UTC()
	if _, err = tx.ExecContext(ctx, `INSERT INTO content_outbox
        (event_id,post_id,event_type,schema_version,author_id,community_id,occurred_at)
        VALUES (?,?,'post.published',1,?,?,?)`, eventID, p.ID, p.AuthorID, p.CommunityID, p.CreateTime); err != nil {
		return err
	}
	for _, kind := range []string{ProjectionIndex, ProjectionFanout} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO content_projection_task(event_id,kind,status) VALUES (?,?,'pending')`, eventID, kind); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClaimProjection serializes selection and claim in one short transaction. DB
// locks never span a Redis call or a fanout batch. SKIP LOCKED requires MySQL 8.
func ClaimProjection(ctx context.Context, kind, owner string, lease time.Duration, maxAttempts int) (*ProjectionTask, error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var task ProjectionTask
	err = tx.GetContext(ctx, &task, `SELECT t.task_id, t.event_id, e.post_id,e.author_id,e.community_id,e.occurred_at,
        t.kind,t.status,t.attempts,t.consecutive_failures,t.generation,t.last_follower_id,t.last_recipient_id,t.scan_upper_id
        FROM content_projection_task t JOIN content_outbox e ON e.event_id=t.event_id
        WHERE t.kind=? AND (
            (t.status IN ('pending','retry') AND t.next_run_at<=UTC_TIMESTAMP(6)) OR
            (t.status='running' AND t.lease_until<=UTC_TIMESTAMP(6)))
        ORDER BY t.task_id LIMIT 1 FOR UPDATE SKIP LOCKED`, kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if task.FailureCount >= maxAttempts {
		if _, err = tx.ExecContext(ctx, `UPDATE content_projection_task SET status='failed',
            last_error='automatic retry limit exhausted',lease_owner=NULL,lease_until=NULL
            WHERE task_id=?`, task.TaskID); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE content_projection_task SET
        attempts=attempts+1,consecutive_failures=consecutive_failures+IF(status='running',1,0),
        generation=generation+1,lease_owner=?,
        lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL ? MICROSECOND),last_error='',status='running'
        WHERE task_id=?`, owner, lease.Microseconds(), task.TaskID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	task.Generation++
	task.Attempts++
	if task.Status == "running" {
		task.FailureCount++
	}
	task.Status = "running"
	task.LeaseOwner = owner
	return &task, nil
}

// leaseGuard must run inside the same transaction as progress writes. The
// FOR UPDATE serializes competing claims; generation and DB time are checked.
func leaseGuardSQL(ctx context.Context, tx interface {
	GetContext(context.Context, interface{}, string, ...interface{}) error
}, task *ProjectionTask) error {
	var generation uint64
	err := tx.GetContext(ctx, &generation, `SELECT generation FROM content_projection_task
        WHERE task_id=? AND kind=? AND status='running' AND lease_owner=?
          AND generation=? AND lease_until>UTC_TIMESTAMP(6) FOR UPDATE`,
		task.TaskID, task.Kind, task.LeaseOwner, task.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrProjectionLeaseLost
	}
	return err
}

func CompleteProjection(ctx context.Context, task *ProjectionTask) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = leaseGuardSQL(ctx, tx, task); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE content_projection_task SET status='done',lease_owner=NULL,
        lease_until=NULL,consecutive_failures=0,completed_at=UTC_TIMESTAMP(6) WHERE task_id=?`, task.TaskID); err != nil {
		return err
	}
	return tx.Commit()
}

// ReleaseProjection cooperatively yields a long fanout after its time budget.
func ReleaseProjection(ctx context.Context, task *ProjectionTask) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = leaseGuardSQL(ctx, tx, task); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE content_projection_task SET status='pending',
        next_run_at=UTC_TIMESTAMP(6),lease_owner=NULL,lease_until=NULL WHERE task_id=?`, task.TaskID); err != nil {
		return err
	}
	return tx.Commit()
}

func RetryProjection(ctx context.Context, task *ProjectionTask, errText string, maxAttempts int, delay time.Duration) (string, error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err = leaseGuardSQL(ctx, tx, task); err != nil {
		return "", err
	}
	if len(errText) > 255 {
		errText = errText[:255]
	}
	status := "retry"
	if task.FailureCount+1 >= maxAttempts {
		status = "failed"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE content_projection_task SET status=?,last_error=?,
        next_run_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL ? MICROSECOND),
        consecutive_failures=consecutive_failures+1,lease_owner=NULL,lease_until=NULL WHERE task_id=?`, status, errText, delay.Microseconds(), task.TaskID); err != nil {
		return "", err
	}
	return status, tx.Commit()
}

// FanoutBatch atomically locks its fenced task, active post and the selected
// relation rows. An inactive post cannot be delivered after deletion commits;
// a stale follower generation cannot overwrite a newer inbox ownership.
func FanoutBatch(ctx context.Context, task *ProjectionTask, batchSize int, lease time.Duration) (inserted int64, done bool, err error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	if err = leaseGuardSQL(ctx, tx, task); err != nil {
		return
	}
	var status int
	err = tx.GetContext(ctx, &status, `SELECT status FROM post WHERE post_id=? FOR UPDATE`, task.PostID)
	if errors.Is(err, sql.ErrNoRows) {
		status = 0
		err = nil
	}
	if err != nil {
		return
	}
	if status != 1 {
		_, err = tx.ExecContext(ctx, `UPDATE content_projection_task SET status='done',consecutive_failures=0,
      lease_owner=NULL,lease_until=NULL,completed_at=UTC_TIMESTAMP(6) WHERE task_id=?`, task.TaskID)
		if err != nil {
			return
		}
		err = tx.Commit()
		return 0, true, err
	}
	if !task.ScanUpperID.Valid {
		var upper sql.NullInt64
		if err = tx.GetContext(ctx, &upper, `SELECT MAX(follower_id) FROM user_follow WHERE followee_id=? AND active=1`, task.AuthorID); err != nil {
			return
		}
		task.ScanUpperID = sql.NullInt64{Int64: upper.Int64, Valid: true}
		if _, err = tx.ExecContext(ctx, `UPDATE content_projection_task SET scan_upper_id=? WHERE task_id=?`, task.ScanUpperID.Int64, task.TaskID); err != nil {
			return
		}
	}
	type edge struct {
		FollowerID int64  `db:"follower_id"`
		Generation uint64 `db:"generation"`
	}
	followers := make([]edge, 0, batchSize)
	if task.ScanUpperID.Int64 > task.LastFollowerID {
		err = tx.SelectContext(ctx, &followers, `SELECT follower_id,generation FROM user_follow WHERE followee_id=?
       AND active=1 AND follower_id>? AND follower_id<=? ORDER BY follower_id LIMIT ? FOR UPDATE`,
			task.AuthorID, task.LastFollowerID, task.ScanUpperID.Int64, batchSize)
		if err != nil {
			return
		}
	}
	for _, e := range followers {
		var r sql.Result
		r, err = tx.ExecContext(ctx, `INSERT INTO feed_inbox(recipient_id,post_id,create_time,author_id,follow_generation)
      VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE
      author_id=IF(follow_generation<=VALUES(follow_generation),VALUES(author_id),author_id),
      create_time=IF(follow_generation<=VALUES(follow_generation),VALUES(create_time),create_time),
      follow_generation=GREATEST(follow_generation,VALUES(follow_generation))`,
			e.FollowerID, task.PostID, task.OccurredAt, task.AuthorID, e.Generation)
		if err != nil {
			return
		}
		var n int64
		n, err = r.RowsAffected()
		if err != nil {
			return
		}
		if n == 1 {
			inserted++
		}
	}
	if len(followers) > 0 {
		task.LastFollowerID = followers[len(followers)-1].FollowerID
	}
	done = len(followers) < batchSize || task.LastFollowerID >= task.ScanUpperID.Int64
	state := "running"
	if done {
		state = "done"
	}
	_, err = tx.ExecContext(ctx, `UPDATE content_projection_task SET last_follower_id=?,status=?,consecutive_failures=0,
    lease_until=IF(?='done',NULL,DATE_ADD(UTC_TIMESTAMP(6),INTERVAL ? MICROSECOND)),
    lease_owner=IF(?='done',NULL,lease_owner),completed_at=IF(?='done',UTC_TIMESTAMP(6),completed_at)
    WHERE task_id=?`, task.LastFollowerID, state, state, lease.Microseconds(), state, state, task.TaskID)
	if err != nil {
		return
	}
	err = tx.Commit()
	if err == nil {
		task.FailureCount = 0
	}
	return
}

// DeleteInboxBatch uses a finite recipient keyset and commits delete/progress
// under the same fenced lease. No user/vote state is removed.
func DeleteInboxBatch(ctx context.Context, task *ProjectionTask, batchSize int, lease time.Duration) (removed int64, done bool, err error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if err = leaseGuardSQL(ctx, tx, task); err != nil {
		return
	}
	ids := make([]int64, 0, batchSize)
	err = tx.SelectContext(ctx, &ids, `SELECT recipient_id FROM feed_inbox WHERE post_id=? AND recipient_id>?
    ORDER BY recipient_id LIMIT ? FOR UPDATE`, task.PostID, task.LastRecipientID, batchSize)
	if err != nil {
		return
	}
	for _, id := range ids {
		var r sql.Result
		r, err = tx.ExecContext(ctx, `DELETE FROM feed_inbox WHERE post_id=? AND recipient_id=?`, task.PostID, id)
		if err != nil {
			return
		}
		var n int64
		n, err = r.RowsAffected()
		if err != nil {
			return
		}
		removed += n
	}
	if len(ids) > 0 {
		task.LastRecipientID = ids[len(ids)-1]
	}
	done = len(ids) < batchSize
	state := "running"
	if done {
		state = "done"
	}
	_, err = tx.ExecContext(ctx, `UPDATE content_projection_task SET last_recipient_id=?,status=?,consecutive_failures=0,
     lease_until=IF(?='done',NULL,DATE_ADD(UTC_TIMESTAMP(6),INTERVAL ? MICROSECOND)),
     lease_owner=IF(?='done',NULL,lease_owner),completed_at=IF(?='done',UTC_TIMESTAMP(6),completed_at) WHERE task_id=?`,
		task.LastRecipientID, state, state, lease.Microseconds(), state, state, task.TaskID)
	if err != nil {
		return
	}
	err = tx.Commit()
	if err == nil {
		task.FailureCount = 0
	}
	return
}

func IsPostIndexPending(ctx context.Context, postID int64) (bool, error) {
	var count int
	err := db.GetContext(ctx, &count, `SELECT COUNT(*) FROM content_projection_task t
        JOIN content_outbox e ON e.event_id=t.event_id JOIN post p ON p.post_id=e.post_id
        WHERE p.post_id=? AND p.status=1 AND t.kind='index' AND t.status IN ('pending','running','retry')`, postID)
	return count > 0, err
}

// UnfinishedIndexPostIDs is consumed by the startup integrity check, not the
// request path. Failed/finished tasks cannot justify missing Redis indexes.
func UnfinishedIndexPostIDs(ctx context.Context) (map[int64]bool, error) {
	ids := make([]int64, 0)
	err := db.SelectContext(ctx, &ids, `SELECT e.post_id FROM content_projection_task t
        JOIN content_outbox e ON e.event_id=t.event_id JOIN post p ON p.post_id=e.post_id
        WHERE t.kind='index' AND t.status IN ('pending','running','retry') AND p.status=1`)
	if err != nil {
		return nil, err
	}
	result := make(map[int64]bool, len(ids))
	for _, id := range ids {
		result[id] = true
	}
	return result, nil
}

// ReadActivePostIDs paginates the startup validation without loading all posts.
func ReadActivePostIDs(ctx context.Context, after int64, limit int) ([]int64, error) {
	ids := make([]int64, 0, limit)
	err := db.SelectContext(ctx, &ids, `SELECT post_id FROM post WHERE status=1 AND post_id>? ORDER BY post_id LIMIT ?`, after, limit)
	return ids, err
}

// ContentTaskSnapshot reports durable global state for one designated sampler.
type ContentTaskSnapshot struct {
	Kind      string  `db:"kind"`
	Status    string  `db:"status"`
	Count     int64   `db:"count"`
	OldestAge float64 `db:"oldest_age"`
}

func ContentTaskSnapshots(ctx context.Context) ([]ContentTaskSnapshot, error) {
	rows := make([]ContentTaskSnapshot, 0, 10)
	err := db.SelectContext(ctx, &rows, `SELECT kind,status,COUNT(*) AS count,
        COALESCE(MAX(IF(status IN ('pending','retry','running'),TIMESTAMPDIFF(SECOND,created_at,UTC_TIMESTAMP()),0)),0) AS oldest_age
        FROM content_projection_task GROUP BY kind,status`)
	return rows, err
}

func ValidateProjectionKind(kind string) error {
	if kind != ProjectionIndex && kind != ProjectionFanout && kind != ProjectionDeleteIndex && kind != ProjectionDeleteInbox {
		return fmt.Errorf("unsupported projection kind %q", kind)
	}
	return nil
}

// errorSummary never includes post text, credentials, or raw SQL.
func ProjectionErrorSummary(err error) string {
	switch {
	case errors.Is(err, ErrProjectionLeaseLost):
		return "lease lost"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "processing timeout/canceled"
	default:
		return "projection operation failed"
	}
}
