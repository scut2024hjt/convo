package mysql

import (
	"errors"

	driver "github.com/go-sql-driver/mysql"
	"github.com/scut2024hjt/convo/models"
)

type VotePersistStatus string

const (
	VotePersisted   VotePersistStatus = "persisted"
	VoteDuplicate   VotePersistStatus = "duplicate"
	VoteSameVersion VotePersistStatus = "same_version"
	VoteStale       VotePersistStatus = "stale"
)

var ErrVoteEventConflict = errors.New("vote event conflicts with persisted version")

// PersistVoteEvent records a consumed message and advances the user's vote only
// when the incoming version is newer. Both writes happen in one transaction.
func PersistVoteEvent(event *models.VoteEvent) (VotePersistStatus, error) {
	tx, err := db.Beginx()
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(
		`INSERT INTO mq_consumed_message (event_id, event_type) VALUES (?, ?)`,
		event.EventID,
		event.EventType,
	)
	if err != nil {
		var mysqlErr *driver.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			if err = tx.Commit(); err != nil {
				return "", err
			}
			return VoteDuplicate, nil
		}
		return "", err
	}

	result, err := tx.Exec(`
		INSERT INTO post_vote
			(user_id, post_id, direction, version, last_event_id, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP(3))
		ON DUPLICATE KEY UPDATE
			direction = IF(VALUES(version) > version, VALUES(direction), direction),
			last_event_id = IF(VALUES(version) > version, VALUES(last_event_id), last_event_id),
			updated_at = IF(VALUES(version) > version, VALUES(updated_at), updated_at),
			version = GREATEST(version, VALUES(version))`,
		event.UserID,
		event.PostID,
		event.Direction,
		event.Version,
		event.EventID,
	)
	if err != nil {
		return "", err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if rowsAffected == 0 {
		var current struct {
			Direction int8  `db:"direction"`
			Version   int64 `db:"version"`
		}
		if err = tx.Get(&current,
			`SELECT direction, version FROM post_vote WHERE user_id = ? AND post_id = ? FOR UPDATE`,
			event.UserID,
			event.PostID,
		); err != nil {
			return "", err
		}
		status := VoteStale
		if current.Version == event.Version {
			if current.Direction != event.Direction {
				return "", ErrVoteEventConflict
			}
			status = VoteSameVersion
		}
		if err = tx.Commit(); err != nil {
			return "", err
		}
		return status, nil
	}

	if err = tx.Commit(); err != nil {
		return "", err
	}
	return VotePersisted, nil
}

func CountPostUpVotes(postID int64) (int64, error) {
	var count int64
	err := db.Get(&count,
		`SELECT COUNT(*) FROM post_vote WHERE post_id = ? AND direction = 1`,
		postID,
	)
	return count, err
}
