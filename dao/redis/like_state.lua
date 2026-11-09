-- Read direction/version at ONE atomic Redis boundary. No read-through write.
local function kind(k)
  local t = redis.call('TYPE', k)
  if type(t) == 'table' then return t.ok end
  return t
end
local function accepts(k, t)
  local actual = kind(k)
  return actual == 'none' or actual == t
end
if not accepts(KEYS[1], 'zset') or not accepts(KEYS[2], 'hash') or
   not accepts(KEYS[3], 'zset') or not accepts(KEYS[4], 'zset') or
   not accepts(KEYS[5], 'string') or not accepts(KEYS[6], 'set') then
  return {5, '0', '0'}
end
if redis.call('GET', KEYS[5]) ~= 'ready:likes:v1' then
  return {6, '0', '0'}
end
if redis.call('SISMEMBER', KEYS[6], ARGV[1]) == 1 or
   not redis.call('ZSCORE', KEYS[3], ARGV[1]) or
   not redis.call('ZSCORE', KEYS[4], ARGV[1]) then
  return {3, '0', '0'}
end
local raw = redis.call('ZSCORE', KEYS[1], ARGV[2])
local direction = tonumber(raw or '0')
local stored_version = redis.call('HGET', KEYS[2], ARGV[2])
local version_raw = stored_version or '0'
local version = tonumber(version_raw)
if (direction ~= 0 and direction ~= 1) or not version or
   (stored_version and version == 0) or (direction == 1 and not stored_version) or
   version < 0 or version > tonumber(ARGV[3]) or version ~= math.floor(version) or
   string.format("%.0f", version) ~= version_raw then
  return {7, '0', '0'}
end
return {0, tostring(direction), version_raw}
