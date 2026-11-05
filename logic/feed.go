package logic

import (
	"errors"
	"time"

	"github.com/scut2024hjt/convo/dao/mysql"
	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/pkg/feedcursor"
)

var ErrSelfFollow = errors.New("cannot follow yourself")
var ErrInvalidFeedCursor = feedcursor.ErrInvalid

func FollowAuthor(followerID, followeeID int64) error {
	if followerID == followeeID {
		return ErrSelfFollow
	}
	return mysql.FollowAuthor(followerID, followeeID)
}

func UnfollowAuthor(followerID, followeeID int64) error {
	return mysql.UnfollowAuthor(followerID, followeeID)
}

func IsFollowing(followerID, followeeID int64) (bool, error) {
	if followerID == followeeID {
		return false, nil
	}
	return mysql.IsFollowing(followerID, followeeID)
}

func encodeFeedCursor(t time.Time, id string) string             { return feedcursor.Encode(t, id) }
func decodeFeedCursor(encoded string) (*time.Time, int64, error) { return feedcursor.Decode(encoded) }

func GetFeed(userID int64, limit int, cursor string) (*models.FeedPage, error) {
	before, id, err := decodeFeedCursor(cursor)
	if err != nil {
		return nil, err
	}
	posts, err := mysql.FeedPosts(userID, limit+1, before, id)
	if err != nil {
		return nil, err
	}
	page := &models.FeedPage{Items: posts}
	if len(posts) > limit {
		page.Items = posts[:limit]
		last := page.Items[len(page.Items)-1]
		cursor := encodeFeedCursor(last.CreateTime, last.ID)
		page.NextCursor = &cursor
	}
	return page, nil
}
