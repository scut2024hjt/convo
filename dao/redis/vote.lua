local function key_type(key)
    local result = redis.call('TYPE', key)
    if type(result) == 'table' then
        return result['ok']
    end
    return result
end

local function type_is(key, expected)
    local actual = key_type(key)
    return actual == 'none' or actual == expected
end

if not type_is(KEYS[1], 'zset')
    or not type_is(KEYS[2], 'zset')
    or not type_is(KEYS[3], 'zset')
    or not type_is(KEYS[4], 'hash')
    or not type_is(KEYS[5], 'stream') then
    return {5, 0, 0, 0, 0, ''}
end

local direction = tonumber(ARGV[3])
if direction ~= -1 and direction ~= 0 and direction ~= 1 then
    return {4, 0, 0, 0, 0, ''}
end

local post_time = redis.call('ZSCORE', KEYS[1], ARGV[1])
local post_score = redis.call('ZSCORE', KEYS[2], ARGV[1])
if not post_time or not post_score then
    return {3, 0, direction, 0, 0, ''}
end

if tonumber(ARGV[5]) - tonumber(post_time) > tonumber(ARGV[6]) then
    return {1, 0, direction, 0, 0, ''}
end

local old_direction = tonumber(redis.call('ZSCORE', KEYS[3], ARGV[2]) or '0')
local current_version = tonumber(redis.call('HGET', KEYS[4], ARGV[2]) or '0')
if old_direction == direction then
    return {2, old_direction, direction, 0, current_version, ''}
end

local delta = direction - old_direction
redis.call('ZINCRBY', KEYS[2], delta * tonumber(ARGV[7]), ARGV[1])
if direction == 0 then
    redis.call('ZREM', KEYS[3], ARGV[2])
else
    redis.call('ZADD', KEYS[3], direction, ARGV[2])
end
local version = redis.call('HINCRBY', KEYS[4], ARGV[2], 1)
local stream_id = redis.call('XADD', KEYS[5], '*',
    'event_id', ARGV[4],
    'event_type', ARGV[8],
    'user_id', ARGV[2],
    'post_id', ARGV[1],
    'direction', tostring(direction),
    'version', tostring(version),
    'occurred_at', ARGV[5])

return {0, old_direction, direction, delta, version, stream_id}
