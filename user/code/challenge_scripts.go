package code

import "github.com/redis/go-redis/v9"

// Failure records use a fixed window. Each physical target digest is read once;
// on a mismatch the merged count moves to the active key with the earliest
// existing expiry. Rotation never creates another window or counts aliases twice.
const challengeBudgetLua = `
local function failureBudget(keys)
  local count = 0
  local expiry = nil
  for _, key in ipairs(keys) do
    local raw = redis.call('GET', key)
    if raw then
      local n = tonumber(raw)
      local ttl = redis.call('PTTL', key)
      if not n or n < 1 or n ~= math.floor(n) or ttl < 0 then
        error('code: invalid failure budget')
      end
      count = count + n
      if not expiry or ttl < expiry then expiry = ttl end
    end
  end
  return count, expiry
end
local function retrySeconds(ttl)
  return math.max(1, math.ceil(ttl / 1000))
end
`

// KEYS: {cooldown, target quota, challenge, failure} per unique digest; IP quota last.
// ARGV: cooldownSec, dailyTarget, dailyIP, HMAC, ttlSec, quotaSec, id, user, session, failureLimit.
var issueChallengeScript = redis.NewScript(challengeBudgetLua + `
-- Lua tonumber accepts forms (1.0, 1e0, +1, 01) that Redis INCR rejects.
-- Validate every writable quota before the first write; script errors cannot
-- roll back the earlier cooldown/quota writes. Compare the int64 bound as text
-- because Lua numbers lose integer precision near that bound.
local function quotaCount(raw)
  if not raw then return 0 end
  if raw ~= '0' and not string.match(raw, '^[1-9][0-9]*$') then
    error('code: invalid quota')
  end
  if #raw > 19 or (#raw == 19 and raw > '9223372036854775806') then
    error('code: quota cannot be incremented')
  end
  return tonumber(raw)
end
local groups = (#KEYS - 1) / 4
local failures = {}
for j = 1, groups do failures[j] = KEYS[(j - 1) * 4 + 4] end
local failed, expiry = failureBudget(failures)
if failed >= tonumber(ARGV[10]) then return {'TARGET_VERIFY_LIMIT', retrySeconds(expiry)} end
local cooldown = 0
for j = 1, groups do
  local ttl = redis.call('PTTL', KEYS[(j - 1) * 4 + 1])
  if ttl == -1 then return redis.error_reply('code: cooldown has no expiry') end
  if ttl >= 0 then cooldown = math.max(cooldown, retrySeconds(ttl)) end
end
if cooldown > 0 then return {'COOLDOWN', cooldown} end
local target = 0
for j = 1, groups do
  local n = quotaCount(redis.call('GET', KEYS[(j - 1) * 4 + 2]))
  target = target + n
end
if target >= tonumber(ARGV[2]) then return {'TARGET_LIMIT', 0} end
local ipKey = KEYS[#KEYS]
local ip = quotaCount(redis.call('GET', ipKey))
if ip >= tonumber(ARGV[3]) then return {'IP_LIMIT', 0} end
redis.call('SET', KEYS[1], '1', 'EX', ARGV[1])
if redis.call('INCR', KEYS[2]) == 1 then redis.call('EXPIRE', KEYS[2], ARGV[6]) end
if redis.call('INCR', ipKey) == 1 then redis.call('EXPIRE', ipKey, ARGV[6]) end
for j = 1, groups do redis.call('DEL', KEYS[(j - 1) * 4 + 3]) end
redis.call('HSET', KEYS[3], 'h', ARGV[4], 'n', '0', 'code_id', ARGV[7], 'user_id', ARGV[8], 'session_id', ARGV[9])
redis.call('EXPIRE', KEYS[3], ARGV[5])
return {'OK', 0}
`)

// KEYS: {challenge, failure} per unique digest. ARGV: id, user, session,
// maxAttempts, failureLimit, failureWindowMS, candidateHMACs...
var verifyChallengeScript = redis.NewScript(challengeBudgetLua + `
local key = nil
local count = 0
local failures = {}
for j = 1, #KEYS, 2 do
  failures[#failures + 1] = KEYS[j + 1]
  if redis.call('EXISTS', KEYS[j]) == 1 then count = count + 1; key = KEYS[j] end
end
if count == 0 then return {'MISSING', 0} end
if count > 1 then
  for j = 1, #KEYS, 2 do redis.call('DEL', KEYS[j]) end
  return {'MISSING', 0}
end
if redis.call('PTTL', key) < 0 then return redis.error_reply('code: challenge has no expiry') end
local id = redis.call('HGET', key, 'code_id')
local user = redis.call('HGET', key, 'user_id')
local session = redis.call('HGET', key, 'session_id')
local stored = redis.call('HGET', key, 'h')
local attempts = tonumber(redis.call('HGET', key, 'n'))
if not id or not user or not session or not stored or not attempts or attempts < 0 or attempts ~= math.floor(attempts) then
  return redis.error_reply('code: invalid challenge')
end
if id ~= ARGV[1] or user ~= ARGV[2] or session ~= ARGV[3] then return {'MISSING', 0} end
if attempts >= tonumber(ARGV[4]) then redis.call('DEL', key); return {'MISSING', 0} end
local failed, expiry = failureBudget(failures)
if failed >= tonumber(ARGV[5]) then return {'TARGET_VERIFY_LIMIT', retrySeconds(expiry)} end
for i = 7, #ARGV do
  if stored == ARGV[i] then redis.call('DEL', key); return {'OK', 0} end
end
-- Both counters change only after the current id/binding and digest checks.
if attempts + 1 >= tonumber(ARGV[4]) then redis.call('DEL', key)
else redis.call('HINCRBY', key, 'n', 1) end
for _, failure in ipairs(failures) do redis.call('DEL', failure) end
redis.call('SET', failures[1], failed + 1, 'PX', math.max(1, expiry or tonumber(ARGV[6])))
if failed + 1 >= tonumber(ARGV[5]) then return {'TARGET_VERIFY_LIMIT', retrySeconds(expiry or tonumber(ARGV[6]))} end
return {'MISMATCH', 0}
`)

var discardChallengeScript = redis.NewScript(`
-- Validate all reads before deleting, since Lua errors do not roll back writes.
local matching = {}
for _, key in ipairs(KEYS) do
  if redis.call('EXISTS', key) == 1 then
    if redis.call('HGET', key, 'code_id') == ARGV[1] then matching[#matching + 1] = key end
  end
end
for _, key in ipairs(matching) do redis.call('DEL', key) end
return {'OK', 0}
`)
