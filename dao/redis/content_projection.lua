-- Atomically initialize only a brand-new post. Never reset an already
-- initialized post's score (possibly advanced by vote.lua) or version state.
-- KEYS: global time zset, global score zset, community set,
--       per-post voted zset, per-post version hash, deleted-post tombstones.
-- ARGV: post id (decimal string), original unix seconds.
if redis.call('TYPE',KEYS[6]).ok~='none' and redis.call('TYPE',KEYS[6]).ok~='set' then return 2 end
if redis.call('SISMEMBER',KEYS[6],ARGV[1])==1 then return 6 end
local expected = {'zset','zset','set','zset','hash'}
for i=1,5 do
  local t=redis.call('TYPE',KEYS[i]).ok
  if t~='none' and t~=expected[i] then return 2 end
end
local timeScore=redis.call('ZSCORE',KEYS[1],ARGV[1])
local hotScore=redis.call('ZSCORE',KEYS[2],ARGV[1])
if (timeScore and not hotScore) or (hotScore and not timeScore) then return 3 end
if not timeScore then
  if redis.call('EXISTS',KEYS[4])==1 or redis.call('EXISTS',KEYS[5])==1 then return 4 end
  -- A dangling community membership is not a fresh post: do not overwrite
  -- any old vote score by treating this incomplete index as new.
  if redis.call('SISMEMBER',KEYS[3],ARGV[1])==1 then return 5 end
  redis.call('ZADD',KEYS[1],'NX',ARGV[2],ARGV[1])
  redis.call('ZADD',KEYS[2],'NX',ARGV[2],ARGV[1])
end
redis.call('SADD',KEYS[3],ARGV[1])
return timeScore and 1 or 0
