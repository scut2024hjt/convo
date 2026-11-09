package mysql

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/pkg/likepolicy"
)

type LikeMigrationProgress struct {
	Phase      string `db:"phase"`
	LastUserID int64  `db:"last_user_id"`
	LastPostID int64  `db:"last_post_id"`
	Scanned    uint64 `db:"scanned_count"`
	Converted  uint64 `db:"converted_count"`
}

func GetLikeMigrationProgress(ctx context.Context) (*LikeMigrationProgress, error) {
	s := new(LikeMigrationProgress)
	err := db.GetContext(ctx, s, `SELECT phase,last_user_id,last_post_id,scanned_count,converted_count FROM like_semantics_state WHERE id=1`)
	return s, err
}

// ConvertLikeBatch scans all rows in lexicographic PK order to establish a
// durable checkpoint. Updating direction/version and checkpoint is ONE commit.
// A second run never increments an already converted -1 row.
func ConvertLikeBatch(ctx context.Context, batch int) (progress *LikeMigrationProgress, changed uint64, done bool, err error) {
	if batch < 1 || batch > 1000 {
		return nil, 0, false, fmt.Errorf("migration batch must be 1..1000")
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, 0, false, err
	}
	defer tx.Rollback()
	s := new(LikeMigrationProgress)
	if err = tx.GetContext(ctx, s, `SELECT phase,last_user_id,last_post_id,scanned_count,converted_count
        FROM like_semantics_state WHERE id=1 FOR UPDATE`); err != nil {
		return nil, 0, false, err
	}
	if s.Phase == "ready" || s.Phase == "converted" {
		return s, 0, true, tx.Commit()
	}
	if s.Phase != "pending" && s.Phase != "migrating" {
		return nil, 0, false, fmt.Errorf("unknown migration phase %s", s.Phase)
	}
	type vote struct {
		UserID    int64  `db:"user_id"`
		PostID    int64  `db:"post_id"`
		Direction int8   `db:"direction"`
		Version   uint64 `db:"version"`
	}
	rows := make([]vote, 0, batch)
	err = tx.SelectContext(ctx, &rows, `SELECT user_id,post_id,direction,version FROM post_vote
      WHERE (user_id>? OR (user_id=? AND post_id>?))
      ORDER BY user_id,post_id LIMIT ? FOR UPDATE`, s.LastUserID, s.LastUserID, s.LastPostID, batch)
	if err != nil {
		return nil, 0, false, err
	}
	if len(rows) == 0 {
		var invalid uint64
		if err = tx.GetContext(ctx, &invalid, `SELECT COUNT(*) FROM post_vote WHERE user_id<=0 OR post_id<=0 OR direction NOT IN (0,1) OR version=0 OR version>?`, models.MaxLikeVersion); err != nil {
			return nil, 0, false, err
		}
		if invalid != 0 {
			return nil, 0, false, fmt.Errorf("%d invalid like states remain", invalid)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE like_semantics_state SET phase='converted' WHERE id=1`); err != nil {
			return nil, 0, false, err
		}
		s.Phase = "converted"
		return s, 0, true, tx.Commit()
	}
	for _, r := range rows {
		if r.UserID <= 0 || r.PostID <= 0 {
			return nil, 0, false, fmt.Errorf("invalid persisted identifiers user=%d post=%d", r.UserID, r.PostID)
		}
		_, _, needsConversion, conversionErr := likepolicy.ConvertLegacyState(r.Direction, r.Version)
		if conversionErr != nil {
			return nil, 0, false, fmt.Errorf("invalid persisted state user=%d post=%d: %w", r.UserID, r.PostID, conversionErr)
		}
		if needsConversion {
			update, e := tx.ExecContext(ctx, `UPDATE post_vote SET direction=0,version=version+1,updated_at=UTC_TIMESTAMP(3)
                  WHERE user_id=? AND post_id=? AND direction=-1 AND version=?`, r.UserID, r.PostID, r.Version)
			if e != nil {
				return nil, 0, false, e
			}
			n, e := update.RowsAffected()
			if e != nil {
				return nil, 0, false, e
			}
			if n != 1 {
				return nil, 0, false, errors.New("vote changed during maintenance; rollback batch")
			}
			changed++
		}
	}
	last := rows[len(rows)-1]
	s.LastUserID = last.UserID
	s.LastPostID = last.PostID
	s.Scanned += uint64(len(rows))
	s.Converted += changed
	s.Phase = "migrating"
	_, err = tx.ExecContext(ctx, `UPDATE like_semantics_state SET phase='migrating',last_user_id=?,last_post_id=?,
      scanned_count=?,converted_count=? WHERE id=1`, s.LastUserID, s.LastPostID, s.Scanned, s.Converted)
	if err != nil {
		return nil, 0, false, err
	}
	return s, changed, false, tx.Commit()
}

// Once all batches pass validation, tighten 001's CHECK. This is an explicit
// DDL step and cannot be part of the preceding transaction. Repeat-safe.
func TightenLikeDirectionCheck(ctx context.Context) error {
	var clause string
	err := db.GetContext(ctx, &clause, `SELECT CHECK_CLAUSE FROM information_schema.CHECK_CONSTRAINTS
       WHERE CONSTRAINT_SCHEMA=DATABASE() AND CONSTRAINT_NAME='chk_post_vote_direction' LIMIT 1`)
	if err != nil {
		return err
	}
	phase, err := LikeMigrationPhase(ctx)
	if err != nil {
		return err
	}
	if phase != "converted" && phase != "ready" {
		return fmt.Errorf("migration %s is incomplete", phase)
	}
	if err = LikeDirectionIntegrity(ctx); err != nil {
		return err
	}
	if isTightLikeCheck(clause) {
		return validateReadyLikeSchema(ctx)
	}
	if !isLegacyLikeCheck(clause) {
		return fmt.Errorf("unknown existing direction CHECK; inspect manually before migration")
	}
	_, err = db.ExecContext(ctx, `ALTER TABLE post_vote DROP CHECK chk_post_vote_direction,
      ADD CONSTRAINT chk_post_vote_direction CHECK (direction IN (0,1))`)
	return err
}

// CHECK_CLAUSE from MySQL 8 may spell negative literals as -(1).
var tightLikeCheck = regexp.MustCompile(`(?i)\bdirection\s+in\s*\(\s*0\s*,\s*1\s*\)`)
var legacyLikeCheck = regexp.MustCompile(`(?i)\bdirection\s+in\s*\(\s*(?:-1|-\s*\(\s*1\s*\))\s*,\s*0\s*,\s*1\s*\)`)

func normalizeCheck(clause string) string { return strings.ReplaceAll(clause, "`", "") }
func isTightLikeCheck(clause string) bool {
	return tightLikeCheck.MatchString(normalizeCheck(clause)) && !legacyLikeCheck.MatchString(normalizeCheck(clause))
}
func isLegacyLikeCheck(clause string) bool {
	return legacyLikeCheck.MatchString(normalizeCheck(clause))
}

func LikeDirectionIntegrity(ctx context.Context) error {
	var bad int64
	if err := db.GetContext(ctx, &bad, `SELECT COUNT(*) FROM post_vote WHERE user_id<=0 OR post_id<=0 OR direction NOT IN (0,1) OR version=0 OR version>?`, models.MaxLikeVersion); err != nil {
		return err
	}
	if bad > 0 {
		return fmt.Errorf("%d invalid persisted like rows", bad)
	}
	return nil
}
