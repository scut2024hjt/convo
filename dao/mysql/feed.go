package mysql

import (
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/scut2024hjt/convo/models"
)

var ErrFollowTargetNotFound = errors.New("follow target does not exist")

// FollowAuthor is idempotent even if concurrent PUTs race on the unique key.
func FollowAuthor(followerID, followeeID int64) error {
	var exists int
	if err := db.Get(&exists, `SELECT 1 FROM user WHERE user_id = ?`, followeeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrFollowTargetNotFound
		}
		return err
	}
	_, err := db.Exec(`INSERT INTO user_follow (follower_id, followee_id)
		VALUES (?, ?) ON DUPLICATE KEY UPDATE followee_id = followee_id`, followerID, followeeID)
	return err
}

// UnfollowAuthor is intentionally successful when no relationship exists.
func UnfollowAuthor(followerID, followeeID int64) error {
	_, err := db.Exec(`DELETE FROM user_follow WHERE follower_id = ? AND followee_id = ?`, followerID, followeeID)
	return err
}

func IsFollowing(followerID, followeeID int64) (bool, error) {
	var count int
	err := db.Get(&count, `SELECT COUNT(*) FROM user_follow WHERE follower_id = ? AND followee_id = ?`, followerID, followeeID)
	return count != 0, err
}

// FeedPosts performs one parameterized query including author/community data.
// The full (create_time, post_id) keyset disambiguates second-level timestamps.
func FeedPosts(followerID int64, limit int, beforeTime *time.Time, beforeID int64) ([]models.FeedItem, error) {
	query := `SELECT p.post_id, p.author_id, u.username AS author_name,
		p.title, p.create_time, c.community_id, c.community_name
		FROM post p
		JOIN user_follow f ON f.followee_id = p.author_id AND f.follower_id = ?
		JOIN user u ON u.user_id = p.author_id
		JOIN community c ON c.community_id = p.community_id
		WHERE p.status = 1`
	args := []interface{}{followerID}
	if beforeTime != nil {
		query += ` AND (p.create_time < FROM_UNIXTIME(?) OR (p.create_time = FROM_UNIXTIME(?) AND p.post_id < ?))`
		args = append(args, beforeTime.Unix(), beforeTime.Unix(), beforeID)
	}
	query += ` ORDER BY p.create_time DESC, p.post_id DESC LIMIT ?`
	args = append(args, limit)
	type row struct {
		PostID        int64     `db:"post_id"`
		AuthorID      int64     `db:"author_id"`
		AuthorName    string    `db:"author_name"`
		Title         string    `db:"title"`
		CreateTime    time.Time `db:"create_time"`
		CommunityID   int64     `db:"community_id"`
		CommunityName string    `db:"community_name"`
	}
	rows := make([]row, 0, limit)
	if err := db.Select(&rows, query, args...); err != nil {
		return nil, err
	}
	items := make([]models.FeedItem, 0, len(rows))
	for _, p := range rows {
		items = append(items, models.FeedItem{
			ID:         strconv.FormatInt(p.PostID, 10),
			AuthorID:   strconv.FormatInt(p.AuthorID, 10),
			AuthorName: p.AuthorName,
			Title:      p.Title,
			CreateTime: p.CreateTime,
			Community:  models.FeedCommunity{ID: strconv.FormatInt(p.CommunityID, 10), Name: p.CommunityName},
		})
	}
	return items, nil
}

// PushFeedPosts reads only the caller's inbox and joins current follow edges
// and visibility, rather than trusting membership from the delivery moment.
// All metadata is returned in a single query (no N+1).
func PushFeedPosts(recipientID int64, limit int, before *time.Time, beforeID int64) ([]models.FeedItem, error) {
	query := `SELECT p.post_id,p.author_id,u.username AS author_name,p.title,p.create_time,
        c.community_id,c.community_name FROM feed_inbox i
        JOIN post p ON p.post_id=i.post_id AND p.status=1
        JOIN user_follow f ON f.follower_id=i.recipient_id AND f.followee_id=p.author_id
        JOIN user u ON u.user_id=p.author_id
        JOIN community c ON c.community_id=p.community_id
        WHERE i.recipient_id=?`
	args := []interface{}{recipientID}
	if before != nil {
		query += ` AND (i.create_time<FROM_UNIXTIME(?) OR (i.create_time=FROM_UNIXTIME(?) AND i.post_id<?))`
		args = append(args, before.Unix(), before.Unix(), beforeID)
	}
	query += ` ORDER BY i.create_time DESC,i.post_id DESC LIMIT ?`
	args = append(args, limit)
	type row struct {
		PostID        int64     `db:"post_id"`
		AuthorID      int64     `db:"author_id"`
		AuthorName    string    `db:"author_name"`
		Title         string    `db:"title"`
		CreateTime    time.Time `db:"create_time"`
		CommunityID   int64     `db:"community_id"`
		CommunityName string    `db:"community_name"`
	}
	rows := make([]row, 0, limit)
	if err := db.Select(&rows, query, args...); err != nil {
		return nil, err
	}
	items := make([]models.FeedItem, 0, len(rows))
	for _, p := range rows {
		items = append(items, models.FeedItem{ID: strconv.FormatInt(p.PostID, 10), AuthorID: strconv.FormatInt(p.AuthorID, 10),
			AuthorName: p.AuthorName, Title: p.Title, CreateTime: p.CreateTime,
			Community: models.FeedCommunity{ID: strconv.FormatInt(p.CommunityID, 10), Name: p.CommunityName}})
	}
	return items, nil
}
