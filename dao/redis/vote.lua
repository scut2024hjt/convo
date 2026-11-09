-- All preconditions are checked before mutation. Five previous data keys are
-- kept compatible; KEYS[6] gates the new, incompatible like semantics.
local function kind(k)
  local t = redis.call('TYPE', k)
  if type(t) == 'table' then return t.ok end
  return t
end
local function accepts(k, t)
  local actual = kind(k)
  return actual == 'none' or actual == t
end
if not accepts(KEYS[1], 'zset') or not accepts(KEYS[2], 'zset') or
   not accepts(KEYS[3], 'zset') or not accepts(KEYS[4], 'hash') or
   not accepts(KEYS[5], 'stream') or not accepts(KEYS[6], 'string') or
   not accepts(KEYS[7], 'set') then
  return {5, 0, 0, 0, '0', ''}
end
if redis.call('GET', KEYS[6]) ~= 'ready:likes:v1' then
  return {6, 0, 0, 0, '0', ''}
end
local direction = tonumber(ARGV[3])
if direction ~= 0 and direction ~= 1 then
  return {4, 0, 0, 0, '0', ''}
end
if redis.call('SISMEMBER', KEYS[7], ARGV[1]) == 1 then
  return {3, 0, 0, 0, '0', ''}
end
local pub_time = redis.call('ZSCORE', KEYS[1], ARGV[1])
local score = redis.call('ZSCORE', KEYS[2], ARGV[1])
if not pub_time or not score then
  return {3, 0, 0, 0, '0', ''}
end
local raw_direction = redis.call('ZSCORE', KEYS[3], ARGV[2])
local old_direction = tonumber(raw_direction or '0')
local raw_version = redis.call('HGET', KEYS[4], ARGV[2])
local version = tonumber(raw_version or '0')
local max_version = tonumber(ARGV[6])
if (old_direction ~= 0 and old_direction ~= 1) or
   not version or version < 0 or version >= max_version + 1 or
   (raw_version and version == 0) or (old_direction == 1 and not raw_version) or
   version ~= math.floor(version) or (raw_version and string.format("%.0f", version) ~= raw_version) then
  return {7, 0, 0, 0, '0', ''}
end
if old_direction == direction then
  return {2, old_direction, direction, 0, string.format("%.0f", version), ''}
end
if version >= max_version then
  return {7, 0, 0, 0, '0', ''}
end
local delta = direction - old_direction
redis.call('ZINCRBY', KEYS[2], delta * tonumber(ARGV[7]), ARGV[1])
if direction == 0 then
  redis.call('ZREM', KEYS[3], ARGV[2])
else
  redis.call('ZADD', KEYS[3], direction, ARGV[2])
end
local next_version = redis.call('HINCRBY', KEYS[4], ARGV[2], 1)
local stream_id = redis.call('XADD', KEYS[5], '*',
  'event_id', ARGV[4], 'event_type', ARGV[8],
  'user_id', ARGV[2], 'post_id', ARGV[1],
  'direction', tostring(direction), 'version', string.format('%.0f', next_version),
  'occurred_at', ARGV[5])
return {0, old_direction, direction, delta, string.format('%.0f', next_version), stream_id}
