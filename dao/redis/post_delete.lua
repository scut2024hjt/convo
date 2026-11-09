-- Atomic tombstone and index removal. Do NOT delete vote direction/version.
-- KEYS: time zset, hot zset, community set, tombstone set, detail cache.
-- ARGV: decimal post ID. Tombstone is checked by content_projection.lua.
local expected={'zset','zset','set','set'}
for i=1,4 do
 local t=redis.call('TYPE',KEYS[i]).ok
 if t~='none' and t~=expected[i] then return 2 end
end
redis.call('SADD',KEYS[4],ARGV[1])
redis.call('ZREM',KEYS[1],ARGV[1])
redis.call('ZREM',KEYS[2],ARGV[1])
redis.call('SREM',KEYS[3],ARGV[1])
redis.call('DEL',KEYS[5])
return 1
