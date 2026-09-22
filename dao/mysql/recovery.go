package mysql

import (
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/scut2024hjt/convo/models"
)

func CountActivePosts() (int64, error) {
	var count int64
	err := db.Get(&count, `SELECT COUNT(*) FROM post WHERE status = 1`)
	return count, err
}

// GetPostRecoveryBatch reads active posts in keyset order. The aggregate is
// calculated from post_vote's latest state, not from historical deltas.
func GetPostRecoveryBatch(afterPostID int64, limit int) ([]*models.PostRecoverySnapshot, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("recovery batch size must be positive")
	}
	posts := make([]*models.PostRecoverySnapshot, 0, limit)
	err := db.Select(&posts, `
		SELECT p.post_id,
		       p.community_id,
		       UNIX_TIMESTAMP(p.create_time) AS create_unix,
		       COALESCE(SUM(pv.direction), 0) AS net_vote
		FROM post p
		LEFT JOIN post_vote pv ON pv.post_id = p.post_id
		WHERE p.status = 1 AND p.post_id > ?
		GROUP BY p.post_id, p.community_id, p.create_time
		ORDER BY p.post_id
		LIMIT ?`, afterPostID, limit)
	return posts, err
}

func GetVoteRecoverySnapshots(postIDs []int64) ([]*models.VoteRecoverySnapshot, error) {
	if len(postIDs) == 0 {
		return []*models.VoteRecoverySnapshot{}, nil
	}
	query, args, err := sqlx.In(`
		SELECT user_id, post_id, direction, version
		FROM post_vote
		WHERE post_id IN (?)
		ORDER BY post_id, user_id`, postIDs)
	if err != nil {
		return nil, err
	}
	query = db.Rebind(query)
	votes := make([]*models.VoteRecoverySnapshot, 0)
	err = db.Select(&votes, query, args...)
	return votes, err
}
