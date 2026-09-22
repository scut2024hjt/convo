-- 用户级滑动窗口限流（精确型：ZSET 日志）
-- 返回 {是否放行, 窗口内当前请求数, 建议重试等待毫秒}
--
-- KEYS[1] 限流 key，按用户维度隔离
-- ARGV[1] 当前时间戳（毫秒）
-- ARGV[2] 窗口长度（毫秒）
-- ARGV[3] 窗口内允许的最大请求数
-- ARGV[4] 本次请求的唯一成员（毫秒时间戳-随机 nonce）
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]

-- 1. 把滑出窗口的历史请求清掉，只保留 [now-window, now]
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)

-- 2. 统计窗口内剩余请求数
local count = redis.call('ZCARD', key)

-- 3. 超限：不写入，返回最早一次请求的剩余等待时间
if count >= limit then
    local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
    local retry_after = window
    if oldest[2] then
        retry_after = tonumber(oldest[2]) + window - now
        if retry_after < 0 then
            retry_after = 0
        end
    end
    return {0, count, retry_after}
end

-- 4. 放行：记录本次请求并把整个 key 续期到窗口之后
redis.call('ZADD', key, now, member)
redis.call('PEXPIRE', key, window)
return {1, count + 1, 0}
