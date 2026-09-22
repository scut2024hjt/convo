package redis

import (
	"strconv"
	"time"
)

const deleteSessionScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`

func SetSession(userID int64, sessionID string, ttl time.Duration) error {
	key := getRedisKey(KeyAuthSessionPrefix + strconv.FormatInt(userID, 10))
	return client.Set(key, sessionID, ttl).Err()
}

func ValidateSession(userID int64, sessionID string) (bool, error) {
	key := getRedisKey(KeyAuthSessionPrefix + strconv.FormatInt(userID, 10))
	value, err := client.Get(key).Result()
	if err == Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return value == sessionID, nil
}

func DeleteSession(userID int64, sessionID string) error {
	key := getRedisKey(KeyAuthSessionPrefix + strconv.FormatInt(userID, 10))
	return client.Eval(deleteSessionScript, []string{key}, sessionID).Err()
}
