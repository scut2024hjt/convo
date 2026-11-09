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

const scorePerVote = 432

var (
	ErrVoteTimeExpired    = errors.New("obsolete vote expired status")
	ErrPostNotInitialized = errors.New("post not initialized in redis")
	ErrInvalidDirection   = errors.New("only like/unlike targets are accepted")
	ErrVoteKeyType        = errors.New("unexpected redis key type")
	ErrLikeVersionLimit   = errors.New("like version exceeds Lua safe integer bound")
)

//go:embed vote.lua
var voteLua string
var voteScript = redis.NewScript(voteLua)

//go:embed like_state.lua
var likeStateLua string
var likeStateScript = redis.NewScript(likeStateLua)

func CreatePost(postID, communityID int64) error {
	pipeline := client.TxPipeline()
	now := float64(time.Now().Unix())
	pipeline.ZAdd(getRedisKey(KeyPostTimeZSet), redis.Z{Score: now, Member: postID})
	pipeline.ZAdd(getRedisKey(KeyPostScoreZSet), redis.Z{Score: now, Member: postID})
	pipeline.SAdd(getRedisKey(KeyCommentZSetPrefix+strconv.FormatInt(communityID, 10)), postID)
	_, err := pipeline.Exec()
	return err
}

// VoteForPost is the single Redis-first write path for both legacy and new URLs.
func VoteForPost(userID, postID string, direction int8, eventID string, occurredAt int64) (*models.VoteResult, error) {
	keys := []string{
		getRedisKey(KeyPostTimeZSet), getRedisKey(KeyPostScoreZSet),
		getRedisKey(KeyPostVotedZSetPrefix + postID), getRedisKey(KeyPostVoteVersionPrefix + postID),
		getRedisKey(KeyVoteOutboxStream), getRedisKey(KeyVoteStateStatus), getRedisKey(KeyPostDeletedSet),
	}
	result, err := voteScript.Run(client, keys,
		postID, userID, direction, eventID, occurredAt,
		models.MaxLikeVersion, scorePerVote, models.LikeChangedEventType).Result()
	if err != nil {
		return nil, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) < 6 {
		return nil, fmt.Errorf("unexpected like script result: %#v", result)
	}
	code, err := redisInt64(values[0])
	if err != nil {
		return nil, err
	}
	switch code {
	case 0, 2:
		version, e := redisInt64(values[4])
		if e != nil {
			return nil, e
		}
		return &models.VoteResult{Direction: direction, Version: version, Changed: code == 0}, nil
	case 3:
		return nil, ErrPostNotInitialized
	case 4:
		return nil, ErrInvalidDirection
	case 5:
		return nil, ErrVoteKeyType
	case 6:
		return nil, ErrVoteStateUnavailable
	case 7:
		return nil, ErrLikeVersionLimit
	default:
		return nil, fmt.Errorf("unexpected like script code: %d", code)
	}
}

func ReadOwnLikeState(userID, postID string) (*models.LikeState, error) {
	keys := []string{
		getRedisKey(KeyPostVotedZSetPrefix + postID), getRedisKey(KeyPostVoteVersionPrefix + postID),
		getRedisKey(KeyPostTimeZSet), getRedisKey(KeyPostScoreZSet),
		getRedisKey(KeyVoteStateStatus), getRedisKey(KeyPostDeletedSet),
	}
	result, err := likeStateScript.Run(client, keys, postID, userID, models.MaxLikeVersion).Result()
	if err != nil {
		return nil, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) != 3 {
		return nil, fmt.Errorf("unexpected like state result: %#v", result)
	}
	code, err := redisInt64(values[0])
	if err != nil {
		return nil, err
	}
	switch code {
	case 0:
		direction, e := redisInt64(values[1])
		if e != nil {
			return nil, e
		}
		version, e := redisInt64(values[2])
		if e != nil {
			return nil, e
		}
		return &models.LikeState{PostID: postID, Liked: direction == 1,
			Version: strconv.FormatInt(version, 10), Source: "redis", Freshness: "realtime"}, nil
	case 3:
		return nil, ErrPostNotInitialized
	case 5:
		return nil, ErrVoteKeyType
	case 6:
		return nil, ErrVoteStateUnavailable
	case 7:
		return nil, ErrLikeVersionLimit
	default:
		return nil, fmt.Errorf("unexpected like state code %d", code)
	}
}

// Pipeline limits one batch to 50 scripts; each script reads its pair atomically.
func ReadOwnLikeStates(userID string, postIDs []string) ([]*models.LikeState, error) {
	if len(postIDs) > 50 {
		return nil, errors.New("like state batch exceeds 50")
	}
	pipe := client.Pipeline()
	cmds := make([]*redis.Cmd, len(postIDs))
	for i, postID := range postIDs {
		keys := []string{getRedisKey(KeyPostVotedZSetPrefix + postID), getRedisKey(KeyPostVoteVersionPrefix + postID),
			getRedisKey(KeyPostTimeZSet), getRedisKey(KeyPostScoreZSet), getRedisKey(KeyVoteStateStatus), getRedisKey(KeyPostDeletedSet)}
		// EVAL (not EVALSHA) is used in a pipeline: Script.Run cannot
		// handle NOSCRIPT after Exec when Redis has no cached script yet.
		cmds[i] = pipe.Eval(likeStateLua, keys, postID, userID, models.MaxLikeVersion)
	}
	if _, err := pipe.Exec(); err != nil {
		return nil, err
	}
	states := make([]*models.LikeState, 0, len(postIDs))
	for i, cmd := range cmds {
		raw, err := cmd.Result()
		if err != nil {
			return nil, err
		}
		a, ok := raw.([]interface{})
		if !ok || len(a) != 3 {
			return nil, fmt.Errorf("bad batch result %d", i)
		}
		code, err := redisInt64(a[0])
		if err != nil {
			return nil, err
		}
		switch code {
		case 0:
			dir, err := redisInt64(a[1])
			if err != nil {
				return nil, err
			}
			version, err := redisInt64(a[2])
			if err != nil {
				return nil, err
			}
			states = append(states, &models.LikeState{PostID: postIDs[i], Liked: dir == 1,
				Version: strconv.FormatInt(version, 10), Source: "redis", Freshness: "realtime"})
		case 3:
			states = append(states, &models.LikeState{PostID: postIDs[i], Code: 1014})
		case 5:
			return nil, ErrVoteKeyType
		case 6:
			return nil, ErrVoteStateUnavailable
		case 7:
			return nil, ErrLikeVersionLimit
		default:
			return nil, fmt.Errorf("bad like state code %d", code)
		}
	}
	return states, nil
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
