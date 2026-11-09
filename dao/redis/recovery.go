package redis

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	redigo "github.com/go-redis/redis"
	"github.com/scut2024hjt/convo/models"
)

const (
	VoteStateReady      = "ready:likes:v1"
	VoteStateRebuilding = "rebuilding:likes:v1"
)

var (
	ErrVoteOutboxNotEmpty   = errors.New("vote outbox is not empty")
	ErrVoteStateUnavailable = errors.New("redis post/vote state is not ready")
)

func GetVoteStateStatus() (string, error) {
	status, err := client.Get(getRedisKey(KeyVoteStateStatus)).Result()
	if err == Nil {
		return "", nil
	}
	return status, err
}

func MarkVoteStateReady() error {
	return client.Set(getRedisKey(KeyVoteStateStatus), VoteStateReady, 0).Err()
}

func EnsureVoteStateReadable() error {
	status, err := GetVoteStateStatus()
	if err != nil {
		return err
	}
	if status != VoteStateReady {
		return fmt.Errorf("%w: marker=%q", ErrVoteStateUnavailable, status)
	}
	return nil
}

func CountIndexedPosts() (int64, error) {
	timeCount, err := client.ZCard(getRedisKey(KeyPostTimeZSet)).Result()
	if err != nil {
		return 0, err
	}
	scoreCount, err := client.ZCard(getRedisKey(KeyPostScoreZSet)).Result()
	if err != nil {
		return 0, err
	}
	if timeCount != scoreCount {
		return 0, fmt.Errorf("redis post index count mismatch: time=%d score=%d", timeCount, scoreCount)
	}
	return timeCount, nil
}

// PrepareVoteStateRebuild clears only the Redis structures that are derived
// from MySQL. Sessions, caches, rate-limit keys and the Stream outbox are not
// deleted. The command refuses to start while the outbox still has messages.
func PrepareVoteStateRebuild() error {
	outboxLength, err := client.XLen(getRedisKey(KeyVoteOutboxStream)).Result()
	if err != nil {
		return err
	}
	if outboxLength > 0 {
		return fmt.Errorf("%w: %d entries remain", ErrVoteOutboxNotEmpty, outboxLength)
	}
	if err = client.Set(getRedisKey(KeyVoteStateStatus), VoteStateRebuilding, 0).Err(); err != nil {
		return err
	}
	if err = client.Del(
		getRedisKey(KeyPostTimeZSet),
		getRedisKey(KeyPostScoreZSet),
	).Err(); err != nil {
		return err
	}
	for _, pattern := range []string{
		getRedisKey(KeyPostTimeZSet) + "*",
		getRedisKey(KeyPostScoreZSet) + "*",
		getRedisKey(KeyPostVotedZSetPrefix) + "*",
		getRedisKey(KeyPostVoteVersionPrefix) + "*",
		getRedisKey(KeyCommentZSetPrefix) + "*",
	} {
		if err = deleteKeysMatching(pattern); err != nil {
			return err
		}
	}
	return nil
}

func deleteKeysMatching(pattern string) error {
	for {
		keys := make([]string, 0)
		var cursor uint64
		for {
			batch, next, err := client.Scan(cursor, pattern, 500).Result()
			if err != nil {
				return err
			}
			keys = append(keys, batch...)
			cursor = next
			if cursor == 0 {
				break
			}
		}
		if len(keys) == 0 {
			return nil
		}
		for start := 0; start < len(keys); start += 500 {
			end := start + 500
			if end > len(keys) {
				end = len(keys)
			}
			if err := client.Del(keys[start:end]...).Err(); err != nil {
				return err
			}
		}
	}
}

// RestoreVoteStateBatch appends one MySQL snapshot batch to the rebuilding
// Redis state. Callers must stop application writes for the whole rebuild.
func RestoreVoteStateBatch(posts []*models.PostRecoverySnapshot, votes []*models.VoteRecoverySnapshot) error {
	votesByPost := make(map[int64][]*models.VoteRecoverySnapshot)
	for _, vote := range votes {
		if vote.Direction != 0 && vote.Direction != 1 {
			return fmt.Errorf("invalid persisted direction %d for post %d user %d", vote.Direction, vote.PostID, vote.UserID)
		}
		if vote.Version <= 0 || vote.Version > models.MaxLikeVersion {
			return fmt.Errorf("invalid persisted version %d for post %d user %d", vote.Version, vote.PostID, vote.UserID)
		}
		votesByPost[vote.PostID] = append(votesByPost[vote.PostID], vote)
	}

	pipeline := client.Pipeline()
	for _, post := range posts {
		if post.PostID <= 0 || post.CommunityID <= 0 || post.CreateUnix <= 0 {
			return fmt.Errorf("invalid post recovery snapshot: %#v", post)
		}
		postID := strconv.FormatInt(post.PostID, 10)
		pipeline.ZAdd(getRedisKey(KeyPostTimeZSet), redigo.Z{
			Score: float64(post.CreateUnix), Member: postID,
		})
		pipeline.ZAdd(getRedisKey(KeyPostScoreZSet), redigo.Z{
			Score: float64(post.CreateUnix + post.NetVote*scorePerVote), Member: postID,
		})
		pipeline.SAdd(
			getRedisKey(KeyCommentZSetPrefix+strconv.FormatInt(post.CommunityID, 10)),
			postID,
		)
		for _, vote := range votesByPost[post.PostID] {
			userID := strconv.FormatInt(vote.UserID, 10)
			if vote.Direction != 0 {
				pipeline.ZAdd(getRedisKey(KeyPostVotedZSetPrefix+postID), redigo.Z{
					Score: float64(vote.Direction), Member: userID,
				})
			}
			pipeline.HSet(
				getRedisKey(KeyPostVoteVersionPrefix+postID),
				userID,
				vote.Version,
			)
		}
	}
	_, err := pipeline.Exec()
	return err
}

// VerifyRestoredLikeBatch checks the written snapshot BEFORE a new ready marker
// can be published. It is an offline verification, never an API query. It
// requires every current user/post version including zero-direction tombstones.
func VerifyRestoredLikeBatch(posts []*models.PostRecoverySnapshot, votes []*models.VoteRecoverySnapshot) error {
	byPost := make(map[int64][]*models.VoteRecoverySnapshot, len(posts))
	for _, v := range votes {
		byPost[v.PostID] = append(byPost[v.PostID], v)
	}
	for _, post := range posts {
		id := strconv.FormatInt(post.PostID, 10)
		timeScore, err := client.ZScore(getRedisKey(KeyPostTimeZSet), id).Result()
		if err != nil || timeScore != float64(post.CreateUnix) {
			return fmt.Errorf("time index mismatch for post %s: %v", id, err)
		}
		want := float64(post.CreateUnix + post.NetVote*scorePerVote)
		score, err := client.ZScore(getRedisKey(KeyPostScoreZSet), id).Result()
		if err != nil || score != want {
			return fmt.Errorf("score mismatch for post %s: %v", id, err)
		}
		votedKey := getRedisKey(KeyPostVotedZSetPrefix + id)
		cardinality, err := client.ZCard(votedKey).Result()
		if err != nil || cardinality != post.NetVote {
			return fmt.Errorf("like count mismatch for post %s: %v", id, err)
		}
		for _, v := range byPost[post.PostID] {
			uid := strconv.FormatInt(v.UserID, 10)
			ver, err := client.HGet(getRedisKey(KeyPostVoteVersionPrefix+id), uid).Int64()
			if err != nil || ver != v.Version {
				return fmt.Errorf("version mismatch for post %s: %v", id, err)
			}
			direction, err := client.ZScore(votedKey, uid).Result()
			if v.Direction == 1 {
				if err != nil || direction != 1 {
					return fmt.Errorf("liked user state mismatch for post %s: %v", id, err)
				}
			} else if err != Nil {
				return fmt.Errorf("cancelled user still has like for post %s: %v", id, err)
			}
		}
	}
	return nil
}

// Fresh init is allowed only when Redis DB is truly empty, not when a legacy
// marker disappeared from a live database. Unknown data requires maintenance.
func IsFreshEmptyRedisDB() (bool, error) { n, err := client.DBSize().Result(); return n == 0, err }

// DrainVoteOutbox also checks the consumer group PEL; unconfirmed deliveries
// must not be silently treated as an empty stream.
func DrainVoteOutbox() (int64, int64, error) {
	n, err := client.XLen(getRedisKey(KeyVoteOutboxStream)).Result()
	if err != nil {
		return 0, 0, err
	}
	pending, err := client.XPending(getRedisKey(KeyVoteOutboxStream), VoteOutboxGroup).Result()
	if err != nil {
		// A brand-new stream may have no group. This is the only allowed case.
		if n == 0 && strings.Contains(err.Error(), "NOGROUP") {
			return n, 0, nil
		}
		return n, 0, err
	}
	return n, pending.Count, nil
}
