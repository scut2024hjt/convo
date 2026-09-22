package redis

import (
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis"
	"github.com/scut2024hjt/convo/models"
)

const (
	oneWeekInSeconds = 7 * 24 * 3600
	scorePerVote     = 432
)

var (
	ErrVoteTimeExpired    = errors.New("vote time expired")
	ErrPostNotInitialized = errors.New("post is not initialized in redis")
	ErrInvalidDirection   = errors.New("invalid vote direction")
	ErrVoteKeyType        = errors.New("unexpected redis key type")
)

//go:embed vote.lua
var voteLua string

var voteScript = redis.NewScript(voteLua)

func CreatePost(postID, communityID int64) error {
	pipeline := client.TxPipeline()
	now := float64(time.Now().Unix())
	pipeline.ZAdd(getRedisKey(KeyPostTimeZSet), redis.Z{Score: now, Member: postID})
	pipeline.ZAdd(getRedisKey(KeyPostScoreZSet), redis.Z{Score: now, Member: postID})
	pipeline.SAdd(getRedisKey(KeyCommentZSetPrefix+strconv.FormatInt(communityID, 10)), postID)
	_, err := pipeline.Exec()
	return err
}

func VoteForPost(userID, postID string, direction int8, eventID string, occurredAt int64) (*models.VoteResult, error) {
	keys := []string{
		getRedisKey(KeyPostTimeZSet),
		getRedisKey(KeyPostScoreZSet),
		getRedisKey(KeyPostVotedZSetPrefix + postID),
		getRedisKey(KeyPostVoteVersionPrefix + postID),
		getRedisKey(KeyVoteOutboxStream),
	}
	result, err := voteScript.Run(client, keys,
		postID, userID, direction, eventID, occurredAt,
		oneWeekInSeconds, scorePerVote, models.VoteChangedEventType,
	).Result()
	if err != nil {
		return nil, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) < 6 {
		return nil, fmt.Errorf("unexpected vote script result: %#v", result)
	}
	code, err := redisInt64(values[0])
	if err != nil {
		return nil, err
	}
	version, err := redisInt64(values[4])
	if err != nil {
		return nil, err
	}
	switch code {
	case 0:
		return &models.VoteResult{Direction: direction, Version: version, Changed: true}, nil
	case 1:
		return nil, ErrVoteTimeExpired
	case 2:
		return &models.VoteResult{Direction: direction, Version: version, Changed: false}, nil
	case 3:
		return nil, ErrPostNotInitialized
	case 4:
		return nil, ErrInvalidDirection
	case 5:
		return nil, ErrVoteKeyType
	default:
		return nil, fmt.Errorf("unknown vote script code: %d", code)
	}
}

func redisInt64(value interface{}) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	case []byte:
		return strconv.ParseInt(string(v), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected redis integer type %T", value)
	}
}
