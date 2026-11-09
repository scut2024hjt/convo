package mysql

import (
	"database/sql"
	"errors"
	"strconv"

	driver "github.com/go-sql-driver/mysql"
	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/pkg/likepolicy"
)

type VotePersistStatus string

const (
	VotePersisted   VotePersistStatus = "persisted"
	VoteDuplicate   VotePersistStatus = "duplicate"
	VoteSameVersion VotePersistStatus = "same_version"
	VoteStale       VotePersistStatus = "stale"
)

var ErrVoteEventConflict = likepolicy.ErrConflict
var ErrLegacyEventAdvance = likepolicy.ErrLegacyAdvance

// Legacy -1 is interpreted as 0 ONLY for stale/same-version comparison. Its
// version may NEVER advance the canonical state after maintenance migration.
// ClassifyLikeEvent shares a pure decision function with offline unit tests.
func ClassifyLikeEvent(eventType string, direction int8, version int64, exists bool, currentDirection int8, currentVersion int64) (VotePersistStatus, error) {
	status, err := likepolicy.Classify(eventType, direction, version, exists, currentDirection, currentVersion)
	return VotePersistStatus(status), err
}

// PersistVoteEvent preserves dedupe and state change in the same SQL transaction.
// An incompatible event stays unconsumed and is delivered to DLQ via confirms.
func PersistVoteEvent(e *models.VoteEvent) (VotePersistStatus, error) {
	tx, err := db.Beginx()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var consumed int
	err = tx.Get(&consumed, `SELECT 1 FROM mq_consumed_message WHERE event_id=?`, e.EventID)
	if err == nil {
		return VoteDuplicate, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	uid, err := strconv.ParseInt(e.UserID, 10, 64)
	if err != nil || uid <= 0 {
		return "", errors.New("invalid user id")
	}
	pid, err := strconv.ParseInt(e.PostID, 10, 64)
	if err != nil || pid <= 0 {
		return "", errors.New("invalid post id")
	}
	var current struct {
		Direction int8  `db:"direction"`
		Version   int64 `db:"version"`
	}
	err = tx.Get(&current, `SELECT direction,version FROM post_vote WHERE user_id=? AND post_id=? FOR UPDATE`, uid, pid)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	status, classErr := ClassifyLikeEvent(e.EventType, e.Direction, e.Version, exists, current.Direction, current.Version)
	if classErr != nil {
		return "", classErr
	}
	if status == VotePersisted {
		// Version updates are conditional even if two first events race to insert.
		res, writeErr := tx.Exec(`INSERT INTO post_vote(user_id,post_id,direction,version,last_event_id,updated_at)
        VALUES(?,?,?,?,?,UTC_TIMESTAMP(3)) ON DUPLICATE KEY UPDATE
        direction=IF(VALUES(version)>version,VALUES(direction),direction),
        last_event_id=IF(VALUES(version)>version,VALUES(last_event_id),last_event_id),
        updated_at=IF(VALUES(version)>version,VALUES(updated_at),updated_at),
        version=GREATEST(version,VALUES(version))`, uid, pid, e.Direction, e.Version, e.EventID)
		if writeErr != nil {
			return "", writeErr
		}
		affected, writeErr := res.RowsAffected()
		if writeErr != nil {
			return "", writeErr
		}
		if affected == 0 {
			status = VoteStale
		} // Concurrent insert advanced first; investigate same-version below.
		if affected == 0 {
			var latest struct {
				Direction int8  `db:"direction"`
				Version   int64 `db:"version"`
			}
			if err = tx.Get(&latest, `SELECT direction,version FROM post_vote WHERE user_id=? AND post_id=? FOR UPDATE`, uid, pid); err != nil {
				return "", err
			}
			status, classErr = ClassifyLikeEvent(e.EventType, e.Direction, e.Version, true, latest.Direction, latest.Version)
			if classErr != nil {
				return "", classErr
			}
		}
	}
	_, err = tx.Exec(`INSERT INTO mq_consumed_message(event_id,event_type) VALUES(?,?)`, e.EventID, e.EventType)
	if err != nil {
		var d *driver.MySQLError
		if errors.As(err, &d) && d.Number == 1062 {
			return VoteDuplicate, tx.Commit()
		}
		return "", err
	}
	return status, tx.Commit()
}

func CountPostUpVotes(postID int64) (int64, error) {
	var n int64
	err := db.Get(&n, `SELECT COUNT(*) FROM post_vote WHERE post_id=? AND direction=1`, postID)
	return n, err
}
