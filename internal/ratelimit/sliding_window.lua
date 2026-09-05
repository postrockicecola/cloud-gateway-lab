-- Atomic sliding window. Redis runs this script as one command,
-- so two gateway replicas cannot interleave the four steps.
--
-- KEYS[1] = sorted set of request timestamps
-- ARGV[1] = now_ms
-- ARGV[2] = window_ms
-- ARGV[3] = limit
-- ARGV[4] = unique member for this request
--
-- Returns {allowed, count}. allowed is 1 or 0.

local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]
local cutoff = now - window

-- 1. drop samples that fell out of the window
redis.call('ZREMRANGEBYSCORE', key, '-inf', cutoff)

-- 2. count remaining samples
local count = redis.call('ZCARD', key)

-- 3. decide
if count >= limit then
  return {0, count}
end

-- 4. record this request
redis.call('ZADD', key, now, member)
redis.call('PEXPIRE', key, window)
return {1, count + 1}
