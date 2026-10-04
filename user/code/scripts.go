package code

import "github.com/redis/go-redis/v9"

// issueScript：KEYS = {cooldown1, quotaTarget1, code1, ..., quotaIP}，active 组在前。
// ARGV = {cooldownSec, dailyTarget, dailyIP, codeHMAC, codeTTLSec, quotaTTLSec}
// 返回 {status, retryAfterSec}：status ∈ OK | COOLDOWN | TARGET_LIMIT | IP_LIMIT。
// 全部检查通过才写入，因此并发请求中至多一个 OK。
var issueScript = redis.NewScript(`
local groups = (#KEYS - 1) / 3
local cooldown = 0
for j = 1, groups do
  local ttl = redis.call('PTTL', KEYS[(j - 1) * 3 + 1])
  if ttl == -1 then return redis.error_reply('code: cooldown has no expiry') end
  if ttl >= 0 then cooldown = math.max(cooldown, math.max(1, math.ceil(ttl / 1000))) end
end
if cooldown > 0 then return {'COOLDOWN', cooldown} end
local target = 0
for j = 1, groups do
  local count = tonumber(redis.call('GET', KEYS[(j - 1) * 3 + 2]) or '0')
  if not count or count < 0 or count ~= math.floor(count) then return redis.error_reply('code: invalid quota') end
  target = target + count
end
if target >= tonumber(ARGV[2]) then return {'TARGET_LIMIT', 0} end
local ipKey = KEYS[#KEYS]
local ip = tonumber(redis.call('GET', ipKey) or '0')
if not ip or ip < 0 or ip ~= math.floor(ip) then return redis.error_reply('code: invalid quota') end
if ip >= tonumber(ARGV[3]) then return {'IP_LIMIT', 0} end
redis.call('SET', KEYS[1], '1', 'EX', ARGV[1])
if redis.call('INCR', KEYS[2]) == 1 then redis.call('EXPIRE', KEYS[2], ARGV[6]) end
if redis.call('INCR', ipKey) == 1 then redis.call('EXPIRE', ipKey, ARGV[6]) end
for j = 1, groups do redis.call('DEL', KEYS[(j - 1) * 3 + 3]) end
redis.call('HSET', KEYS[3], 'h', ARGV[4], 'n', '0')
redis.call('EXPIRE', KEYS[3], ARGV[5])
return {'OK', 0}
`)

// verifyScript：KEYS 为去重后的所有版本 code 键，ARGV = {maxAttempts, digest1, ...}。
// 单份旧记录原地计数、不续 TTL；多份历史冲突记录原子作废，不猜测签发顺序。
// 先计数、再在脚本内部完成摘要比对，比对与删除同在一次脚本执行内完成，避免并发校验之间
// 出现"都读到未删除的码、都判定匹配"的竞态（Go 侧再比对一次已经太晚）。
// 返回 {status, unused}：MISSING（键不存在）、EXHAUSTED（超过上限，键已删）、
// OK（匹配某个候选摘要，键已删）、MISMATCH（都不匹配，键保留供后续尝试）。
var verifyScript = redis.NewScript(`
local key = nil
local count = 0
for _, candidate in ipairs(KEYS) do
  if redis.call('EXISTS', candidate) == 1 then
    count = count + 1
    key = candidate
  end
end
if count == 0 then return {'MISSING', ''} end
if count > 1 then
  for _, candidate in ipairs(KEYS) do redis.call('DEL', candidate) end
  return {'MISSING', ''}
end
if redis.call('PTTL', key) < 0 then return redis.error_reply('code: record has no expiry') end
local stored = redis.call('HGET', key, 'h')
local attempts = tonumber(redis.call('HGET', key, 'n'))
if not stored or not attempts or attempts < 0 or attempts ~= math.floor(attempts) then
  return redis.error_reply('code: invalid record')
end
local n = redis.call('HINCRBY', key, 'n', 1)
if n > tonumber(ARGV[1]) then
  redis.call('DEL', key)
  return {'EXHAUSTED', ''}
end
for i = 2, #ARGV do
  if stored == ARGV[i] then
    redis.call('DEL', key)
    return {'OK', ''}
  end
end
return {'MISMATCH', ''}
`)
