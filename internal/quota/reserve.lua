-- Atomic pre-authorization. Initialize, check, and deduct in one script
-- so two gateway replicas cannot both spend the last token.
--
-- KEYS[1] = account balance
-- ARGV[1] = cost
-- ARGV[2] = default balance when the key is missing
--
-- Returns {allowed, remaining}. allowed is 1 or 0.

local key = KEYS[1]
local cost = tonumber(ARGV[1])
local default_balance = tonumber(ARGV[2])

local current = redis.call('GET', key)
if not current then
  current = default_balance
else
  current = tonumber(current)
end

if current < cost then
  return {0, current}
end

local remaining = current - cost
redis.call('SET', key, remaining)
return {1, remaining}
