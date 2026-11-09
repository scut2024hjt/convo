package mysql

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
	"github.com/scut2024hjt/convo/models"
)

// VisiblePosts guards both Redis and persisted status reads with MySQL's
// authoritative soft-delete state. Never infer visibility from cache.
func VisiblePosts(ctx context.Context, ids []int64) (map[int64]bool, error) {
	result := make(map[int64]bool, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	q, args, err := sqlx.In(`SELECT post_id FROM post WHERE post_id IN (?) AND status=1`, ids)
	if err != nil {
		return nil, err
	}
	var rows []int64
	if err = db.SelectContext(ctx, &rows, db.Rebind(q), args...); err != nil {
		return nil, err
	}
	for _, id := range rows {
		result[id] = true
	}
	return result, nil
}

// PersistedLikeStates reads all requested IDs in ONE query. Missing records are
// version zero; callers must label the snapshot persisted (not realtime).
func PersistedLikeStates(ctx context.Context, userID int64, ids []int64) ([]*models.LikeState, error) {
	type state struct {
		PostID    int64 `db:"post_id"`
		Direction int8  `db:"direction"`
		Version   int64 `db:"version"`
	}
	q, args, err := sqlx.In(`SELECT post_id,direction,version FROM post_vote WHERE user_id=? AND post_id IN (?)`, userID, ids)
	if err != nil {
		return nil, err
	}
	rows := make([]state, 0, len(ids))
	if err = db.SelectContext(ctx, &rows, db.Rebind(q), args...); err != nil {
		return nil, err
	}
	byID := make(map[int64]state, len(rows))
	for _, r := range rows {
		if r.Version <= 0 || r.Version > models.MaxLikeVersion || (r.Direction != 0 && r.Direction != 1) {
			return nil, fmt.Errorf("invalid persisted like state for post %d", r.PostID)
		}
		byID[r.PostID] = r
	}
	out := make([]*models.LikeState, 0, len(ids))
	for _, id := range ids {
		r := byID[id]
		out = append(out, &models.LikeState{
			PostID: strconv.FormatInt(id, 10), Liked: r.Direction == 1,
			Version: strconv.FormatInt(r.Version, 10), Source: "mysql", Freshness: "persisted",
		})
	}
	return out, nil
}

func LikeMigrationPhase(ctx context.Context) (string, error) {
	var phase string
	err := db.GetContext(ctx, &phase, `SELECT phase FROM like_semantics_state WHERE id=1`)
	return phase, err
}
func LikeMigrationReady(ctx context.Context) error {
	phase, err := LikeMigrationPhase(ctx)
	if err != nil {
		return err
	}
	if phase != "ready" {
		return fmt.Errorf("like migration phase %q is not ready", phase)
	}
	return validateReadyLikeSchema(ctx)
}
func LikeMigrationRebuildable(ctx context.Context) error {
	phase, err := LikeMigrationPhase(ctx)
	if err != nil {
		return err
	}
	if phase != "converted" && phase != "ready" {
		return fmt.Errorf("like migration phase %q must be converted first", phase)
	}
	return validateReadyLikeSchema(ctx)
}
func CompleteLikeMigration(ctx context.Context) error {
	if err := LikeMigrationRebuildable(ctx); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `UPDATE like_semantics_state SET phase='ready',updated_at=UTC_TIMESTAMP(6) WHERE id=1 AND phase IN ('converted','ready')`)
	return err
}

// The operator cannot mark a partially upgraded schema ready merely by changing
// the phase row. This is a startup/maintenance check, not a request-path scan.
func validateReadyLikeSchema(ctx context.Context) error {
	var constraint struct {
		Clause   string `db:"clause"`
		Enforced string `db:"enforced"`
	}
	if err := db.GetContext(ctx, &constraint, `SELECT c.CHECK_CLAUSE AS clause, t.ENFORCED AS enforced
 FROM information_schema.CHECK_CONSTRAINTS c JOIN information_schema.TABLE_CONSTRAINTS t
 ON c.CONSTRAINT_SCHEMA=t.CONSTRAINT_SCHEMA AND c.CONSTRAINT_NAME=t.CONSTRAINT_NAME
 WHERE c.CONSTRAINT_SCHEMA=DATABASE() AND t.TABLE_NAME='post_vote'
 AND c.CONSTRAINT_NAME='chk_post_vote_direction' AND t.CONSTRAINT_TYPE='CHECK' LIMIT 1`); err != nil {
		return fmt.Errorf("like CHECK constraint unavailable: %w", err)
	}
	if constraint.Enforced != "YES" || !isTightLikeCheck(constraint.Clause) {
		return fmt.Errorf("legacy vote direction CHECK still active")
	}
	if err := LikeDirectionIntegrity(ctx); err != nil {
		return err
	}
	return nil
}

// PendingLikeIndexes is bounded to the requested missing indexes; it is not a
// full startup task scan or one query per post.
func PendingLikeIndexes(ctx context.Context, ids []int64) (map[int64]bool, error) {
	result := make(map[int64]bool, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	q, args, err := sqlx.In(`SELECT DISTINCT e.post_id FROM content_projection_task t
 JOIN content_outbox e ON e.event_id=t.event_id JOIN post p ON p.post_id=e.post_id
 WHERE p.post_id IN (?) AND p.status=1 AND t.kind='index' AND t.status IN ('pending','running','retry')`, ids)
	if err != nil {
		return nil, err
	}
	var rows []int64
	if err = db.SelectContext(ctx, &rows, db.Rebind(q), args...); err != nil {
		return nil, err
	}
	for _, id := range rows {
		result[id] = true
	}
	return result, nil
}
