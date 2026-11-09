package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

const (
	FollowBackfill = "backfill"
	FollowCleanup  = "cleanup"
)

var ErrFollowLeaseLost = errors.New("follow delivery lease lost")

type FollowTask struct {
	TaskID          int64        `db:"task_id"`
	FollowerID      int64        `db:"follower_id"`
	FolloweeID      int64        `db:"followee_id"`
	Generation      uint64       `db:"generation"`
	Kind            string       `db:"kind"`
	Status          string       `db:"status"`
	Attempts        int          `db:"attempts"`
	FailureCount    int          `db:"consecutive_failures"`
	ClaimGeneration uint64       `db:"claim_generation"`
	LeaseOwner      string       `db:"lease_owner"`
	UpperTime       sql.NullTime `db:"upper_created_at"`
	UpperPostID     int64        `db:"upper_post_id"`
	LastTime        sql.NullTime `db:"last_created_at"`
	LastPostID      int64        `db:"last_post_id"`
}

// The author row is locked by follow/bootstrap callers before this locking
// read. This current read is after any earlier publisher commits. Read post
// before locking the edge, matching fanout's post-before-edge lock order.
type backfillUpper struct {
	PostID     int64     `db:"post_id"`
	CreateTime time.Time `db:"create_time"`
}

func readBackfillUpper(ctx context.Context, tx *sqlx.Tx, author int64) (*backfillUpper, error) {
	var top backfillUpper
	err := tx.GetContext(ctx, &top, `SELECT post_id,create_time FROM post
     WHERE author_id=? AND status=1 ORDER BY create_time DESC,post_id DESC LIMIT 1 FOR SHARE`, author)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &top, nil
}

// Called after the author and selected post have been locked; task creation
// and the relation's new generation commit in the same MySQL transaction.
func insertBackfillTask(ctx context.Context, tx *sqlx.Tx, follower, author int64, generation uint64, top *backfillUpper) error {
	var upperTime interface{}
	var upperPostID int64
	if top != nil {
		upperTime, upperPostID = top.CreateTime.UTC(), top.PostID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO follow_delivery_task
    (follower_id,followee_id,generation,kind,status,upper_created_at,upper_post_id)
    VALUES(?,?,?,'backfill','pending',?,?) ON DUPLICATE KEY UPDATE task_id=task_id`,
		follower, author, generation, upperTime, upperPostID)
	return err
}

func ClaimFollowTask(ctx context.Context, kind, owner string, lease time.Duration, maxFailures int) (*FollowTask, error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var t FollowTask
	err = tx.GetContext(ctx, &t, `SELECT task_id,follower_id,followee_id,generation,kind,status,attempts,
     consecutive_failures,claim_generation,upper_created_at,upper_post_id,last_created_at,last_post_id
     FROM follow_delivery_task WHERE kind=? AND
     ((status IN ('pending','retry') AND next_run_at<=UTC_TIMESTAMP(6)) OR
     (status='running' AND lease_until<=UTC_TIMESTAMP(6)))
     ORDER BY task_id LIMIT 1 FOR UPDATE SKIP LOCKED`, kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t.FailureCount >= maxFailures {
		_, err = tx.ExecContext(ctx, `UPDATE follow_delivery_task SET status='failed',last_error='retry limit',lease_owner=NULL,lease_until=NULL WHERE task_id=?`, t.TaskID)
		if err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `UPDATE follow_delivery_task SET attempts=attempts+1,
     consecutive_failures=consecutive_failures+IF(status='running',1,0),
     claim_generation=claim_generation+1,lease_owner=?,
     lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL ? MICROSECOND),status='running' WHERE task_id=?`, owner, lease.Microseconds(), t.TaskID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if t.Status == "running" {
		t.FailureCount++
	}
	t.Attempts++
	t.ClaimGeneration++
	t.Status = "running"
	t.LeaseOwner = owner
	return &t, nil
}

func guardFollowTask(ctx context.Context, tx *sqlx.Tx, t *FollowTask) error {
	var gen uint64
	err := tx.GetContext(ctx, &gen, `SELECT claim_generation FROM follow_delivery_task
     WHERE task_id=? AND kind=? AND status='running' AND lease_owner=? AND
     claim_generation=? AND lease_until>UTC_TIMESTAMP(6) FOR UPDATE`,
		t.TaskID, t.Kind, t.LeaseOwner, t.ClaimGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrFollowLeaseLost
	}
	return err
}

func markFollowTask(ctx context.Context, tx *sqlx.Tx, t *FollowTask, status string, lease time.Duration) error {
	_, err := tx.ExecContext(ctx, `UPDATE follow_delivery_task SET status=?,last_created_at=?,last_post_id=?,
   consecutive_failures=0,lease_until=IF(?='running',DATE_ADD(UTC_TIMESTAMP(6),INTERVAL ? MICROSECOND),NULL),
   lease_owner=IF(?='running',lease_owner,NULL),completed_at=IF(? IN ('done','canceled'),UTC_TIMESTAMP(6),NULL)
   WHERE task_id=?`, status, nullableTime(t.LastTime), t.LastPostID, status, lease.Microseconds(), status, status, t.TaskID)
	return err
}
func nullableTime(t sql.NullTime) interface{} {
	if t.Valid {
		return t.Time.UTC()
	}
	return nil
}

// A committed progress batch breaks the consecutive-failure streak in both
// the database and the object used by this worker's next retry decision.
func commitFollowBatch(tx *sqlx.Tx, task *FollowTask) error {
	if err := tx.Commit(); err != nil {
		return err
	}
	task.FailureCount = 0
	return nil
}

// Each progress batch is a single transaction with the fenced task and current
// edge locked. Old generations cannot insert or overwrite newer inbox versions.
func FollowTaskBatch(ctx context.Context, t *FollowTask, batchSize int, lease time.Duration) (n int64, done bool, canceled bool, err error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, false, false, err
	}
	defer tx.Rollback()
	if err = guardFollowTask(ctx, tx, t); err != nil {
		return
	}
	if t.Kind == FollowBackfill {
		// Post share-locks come before the edge lock. This matches fanout and
		// the online follow path and fences a concurrent soft-delete: deleted
		// content cannot be inserted after its delete-inbox task completes.
		type postRef struct {
			PostID     int64     `db:"post_id"`
			CreateTime time.Time `db:"create_time"`
		}
		rows := make([]postRef, 0, batchSize)
		if t.UpperTime.Valid {
			query := `SELECT post_id,create_time FROM post WHERE author_id=? AND status=1
			AND (create_time<? OR (create_time=? AND post_id<=?))`
			args := []interface{}{t.FolloweeID, t.UpperTime.Time, t.UpperTime.Time, t.UpperPostID}
			if t.LastTime.Valid {
				query += ` AND (create_time<? OR (create_time=? AND post_id<?))`
				args = append(args, t.LastTime.Time, t.LastTime.Time, t.LastPostID)
			}
			query += ` ORDER BY create_time DESC,post_id DESC LIMIT ? FOR SHARE`
			args = append(args, batchSize)
			if err = tx.SelectContext(ctx, &rows, query, args...); err != nil {
				return
			}
		}
		var edge struct {
			Active     bool   `db:"active"`
			Generation uint64 `db:"generation"`
		}
		err = tx.GetContext(ctx, &edge, `SELECT active,generation FROM user_follow WHERE follower_id=? AND followee_id=? FOR UPDATE`, t.FollowerID, t.FolloweeID)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (!edge.Active || edge.Generation != t.Generation) {
			err = markFollowTask(ctx, tx, t, "canceled", lease)
			if err != nil {
				return
			}
			err = commitFollowBatch(tx, t)
			return 0, true, true, err
		}
		if err != nil {
			return
		}
		if !t.UpperTime.Valid {
			err = markFollowTask(ctx, tx, t, "done", lease)
			if err != nil {
				return
			}
			err = commitFollowBatch(tx, t)
			return 0, true, false, err
		}
		for _, p := range rows {
			r, e := tx.ExecContext(ctx, `INSERT INTO feed_inbox(recipient_id,post_id,create_time,author_id,follow_generation)
       VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE
       author_id=IF(follow_generation<=VALUES(follow_generation),VALUES(author_id),author_id),
       create_time=IF(follow_generation<=VALUES(follow_generation),VALUES(create_time),create_time),
       follow_generation=GREATEST(follow_generation,VALUES(follow_generation))`,
				t.FollowerID, p.PostID, p.CreateTime, t.FolloweeID, t.Generation)
			if e != nil {
				err = e
				return
			}
			if affected, e := r.RowsAffected(); e == nil && affected == 1 {
				n++
			} else if e != nil {
				err = e
				return
			}
		}
		if len(rows) > 0 {
			t.LastTime = sql.NullTime{Time: rows[len(rows)-1].CreateTime, Valid: true}
			t.LastPostID = rows[len(rows)-1].PostID
		}
		done = len(rows) < batchSize
	} else if t.Kind == FollowCleanup {
		// Cleanup only the ended generation. A delayed cleanup can never delete a
		// re-follow's newer row, even when the primary key is the same.
		ids := make([]int64, 0, batchSize)
		err = tx.SelectContext(ctx, &ids, `SELECT post_id FROM feed_inbox WHERE recipient_id=?
      AND author_id=? AND follow_generation=? AND post_id>? ORDER BY post_id LIMIT ? FOR UPDATE`,
			t.FollowerID, t.FolloweeID, t.Generation, t.LastPostID, batchSize)
		if err != nil {
			return
		}
		for _, postID := range ids {
			r, e := tx.ExecContext(ctx, `DELETE FROM feed_inbox WHERE recipient_id=? AND post_id=? AND author_id=? AND follow_generation=?`,
				t.FollowerID, postID, t.FolloweeID, t.Generation)
			if e != nil {
				err = e
				return
			}
			if affected, e := r.RowsAffected(); e == nil {
				n += affected
			} else {
				err = e
				return
			}
		}
		if len(ids) > 0 {
			t.LastPostID = ids[len(ids)-1]
		}
		done = len(ids) < batchSize
	} else {
		err = fmt.Errorf("unknown follow task kind %q", t.Kind)
		return
	}
	status := "running"
	if done {
		status = "done"
	}
	err = markFollowTask(ctx, tx, t, status, lease)
	if err != nil {
		return
	}
	err = commitFollowBatch(tx, t)
	return
}

func ReleaseFollowTask(ctx context.Context, t *FollowTask) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = guardFollowTask(ctx, tx, t); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE follow_delivery_task SET status='pending',next_run_at=UTC_TIMESTAMP(6),lease_owner=NULL,lease_until=NULL WHERE task_id=?`, t.TaskID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func RetryFollowTask(ctx context.Context, t *FollowTask, maxFailures int, delay time.Duration) (string, error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err = guardFollowTask(ctx, tx, t); err != nil {
		return "", err
	}
	status := "retry"
	if t.FailureCount+1 >= maxFailures {
		status = "failed"
	}
	_, err = tx.ExecContext(ctx, `UPDATE follow_delivery_task SET status=?,consecutive_failures=consecutive_failures+1,
     next_run_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL ? MICROSECOND),last_error='follow task operation failed',
     lease_owner=NULL,lease_until=NULL WHERE task_id=?`, status, delay.Microseconds(), t.TaskID)
	if err != nil {
		return "", err
	}
	return status, tx.Commit()
}

// BootstrapFollowTasks operates only when explicitly called by the operator.
// Checkpoint updates and idempotent task insertions are committed together.
// A fixed high pair makes the scan finite while new online follows create
// their own tasks. Call repeatedly until done; process may be interrupted.
func BootstrapFollowTasks(ctx context.Context, batchSize int) (created int, done bool, err error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var state struct {
		UpperFollower sql.NullInt64 `db:"upper_follower_id"`
		UpperFollowee sql.NullInt64 `db:"upper_followee_id"`
		LastFollower  int64         `db:"last_follower_id"`
		LastFollowee  int64         `db:"last_followee_id"`
		Initialized   bool          `db:"initialized"`
		Completed     bool          `db:"completed"`
	}
	err = tx.GetContext(ctx, &state, `SELECT upper_follower_id,upper_followee_id,last_follower_id,last_followee_id,
     initialized,completed FROM follow_bootstrap_state WHERE id=1 FOR UPDATE`)
	if err != nil {
		return
	}
	if state.Completed {
		return 0, true, nil
	}
	if !state.Initialized {
		type pair struct {
			FollowerID int64 `db:"follower_id"`
			FolloweeID int64 `db:"followee_id"`
		}
		var upper pair
		e := tx.GetContext(ctx, &upper, `SELECT follower_id,followee_id FROM user_follow ORDER BY follower_id DESC,followee_id DESC LIMIT 1`)
		if errors.Is(e, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx, `UPDATE follow_bootstrap_state SET initialized=1,completed=1 WHERE id=1`)
			if err != nil {
				return
			}
			err = tx.Commit()
			return 0, true, err
		}
		if e != nil {
			err = e
			return
		}
		state.UpperFollower = sql.NullInt64{Int64: upper.FollowerID, Valid: true}
		state.UpperFollowee = sql.NullInt64{Int64: upper.FolloweeID, Valid: true}
		_, err = tx.ExecContext(ctx, `UPDATE follow_bootstrap_state SET initialized=1,upper_follower_id=?,upper_followee_id=? WHERE id=1`, upper.FollowerID, upper.FolloweeID)
		if err != nil {
			return
		}
	}
	type pair struct {
		FollowerID int64 `db:"follower_id"`
		FolloweeID int64 `db:"followee_id"`
	}
	pairs := make([]pair, 0, batchSize)
	err = tx.SelectContext(ctx, &pairs, `SELECT follower_id,followee_id FROM user_follow
    WHERE (follower_id>? OR (follower_id=? AND followee_id>?))
    AND (follower_id<? OR (follower_id=? AND followee_id<=?))
    ORDER BY follower_id,followee_id LIMIT ?`,
		state.LastFollower, state.LastFollower, state.LastFollowee,
		state.UpperFollower.Int64, state.UpperFollower.Int64, state.UpperFollowee.Int64, batchSize)
	if err != nil {
		return
	}
	for _, p := range pairs {
		var author int64
		err = tx.GetContext(ctx, &author, `SELECT user_id FROM user WHERE user_id=? FOR UPDATE`, p.FolloweeID)
		if err != nil {
			return
		}
		upper, readErr := readBackfillUpper(ctx, tx, p.FolloweeID)
		if readErr != nil {
			err = readErr
			return
		}
		var edge struct {
			Active     bool   `db:"active"`
			Generation uint64 `db:"generation"`
		}
		err = tx.GetContext(ctx, &edge, `SELECT active,generation FROM user_follow WHERE follower_id=? AND followee_id=? FOR UPDATE`, p.FollowerID, p.FolloweeID)
		if err != nil {
			return
		}
		if edge.Active {
			if err = insertBackfillTask(ctx, tx, p.FollowerID, p.FolloweeID, edge.Generation, upper); err != nil {
				return
			}
			created++
		}
		state.LastFollower = p.FollowerID
		state.LastFollowee = p.FolloweeID
	}
	done = len(pairs) < batchSize
	_, err = tx.ExecContext(ctx, `UPDATE follow_bootstrap_state SET last_follower_id=?,last_followee_id=?,completed=? WHERE id=1`,
		state.LastFollower, state.LastFollowee, done)
	if err != nil {
		return
	}
	err = tx.Commit()
	return
}

type FollowTaskCount struct {
	Kind      string  `db:"kind"`
	Status    string  `db:"status"`
	Count     int64   `db:"count"`
	OldestAge float64 `db:"oldest_age"`
}

func FollowTaskSnapshots(ctx context.Context) ([]FollowTaskCount, error) {
	rows := []FollowTaskCount{}
	err := db.SelectContext(ctx, &rows, `SELECT kind,status,COUNT(*) AS count,
    COALESCE(MAX(IF(status IN ('pending','running','retry'),TIMESTAMPDIFF(SECOND,created_at,UTC_TIMESTAMP()),0)),0) AS oldest_age
    FROM follow_delivery_task GROUP BY kind,status`)
	return rows, err
}
