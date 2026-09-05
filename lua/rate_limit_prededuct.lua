-- Atomic sliding-window rate limit + token pre-deduct.
-- Redis runs this as one command so two gateway replicas cannot
-- interleave evict → count → balance check → deduct → record.
--
-- KEYS[1] = sorted set of request timestamps (rate-limit window)
-- KEYS[2] = user token balance
-- ARGV[1] = now_us
-- ARGV[2] = window_us
-- ARGV[3] = rate limit N
-- ARGV[4] = tokens to pre-deduct
--
-- Returns {status, remaining_balance}.
-- status is OK | RATE_LIMIT_EXCEEDED | INSUFFICIENT_BALANCE.

local rl_key = KEYS[1]
local bal_key = KEYS[2]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local tokens = tonumber(ARGV[4])
local cutoff = now - window

redis.call('ZREMRANGEBYSCORE', rl_key, '-inf', cutoff)

local count = redis.call('ZCARD', rl_key)
if count >= limit then
  return {'RATE_LIMIT_EXCEEDED', 0}
end

local raw = redis.call('GET', bal_key)
if not raw then
  return {'INSUFFICIENT_BALANCE', 0}
end

local balance = tonumber(raw)
if balance < tokens then
  return {'INSUFFICIENT_BALANCE', balance}
end

local remaining = redis.call('DECRBY', bal_key, tokens)

-- Member must be unique inside the window; Lua is serial so count+1 is unique.
redis.call('ZADD', rl_key, now, tostring(now) .. '-' .. tostring(count + 1))

local expire_sec = math.ceil(window / 1000000)
if expire_sec < 1 then
  expire_sec = 1
end
redis.call('EXPIRE', rl_key, expire_sec)

return {'OK', remaining}
