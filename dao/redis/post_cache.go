package redis

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/scut2024hjt/convo/models"
)

func postCacheKey(postID int64) string {
	return getRedisKey(KeyPostCachePrefix + strconv.FormatInt(postID, 10) + ":v1")
}

func GetPostDetailCache(postID int64) (*models.CachedPostDetail, error) {
	data, err := client.Get(postCacheKey(postID)).Bytes()
	if err != nil {
		return nil, err
	}
	detail := new(models.CachedPostDetail)
	if err := json.Unmarshal(data, detail); err != nil {
		return nil, err
	}
	return detail, nil
}

func SetPostDetailCache(postID int64, detail *models.CachedPostDetail, ttl time.Duration) error {
	data, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return client.Set(postCacheKey(postID), data, ttl).Err()
}

func DeletePostDetailCache(postID int64) error {
	return client.Del(postCacheKey(postID)).Err()
}

func GetPostVoteCount(postID int64) (int64, error) {
	if err := EnsureVoteStateReadable(); err != nil {
		return 0, err
	}
	post := strconv.FormatInt(postID, 10)
	if _, err := client.ZScore(getRedisKey(KeyPostTimeZSet), post).Result(); err != nil {
		if err == Nil {
			return 0, ErrPostNotInitialized
		}
		return 0, err
	}
	key := getRedisKey(KeyPostVotedZSetPrefix + post)
	return client.ZCount(key, "1", "1").Result()
}
