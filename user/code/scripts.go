package code

import "github.com/redis/go-redis/v9"

// issueScript：KEYS = {cooldown, quotaTarget, quotaIP, code}
// ARGV = {cooldownSec, dailyTarget, dailyIP, codeHMAC, codeTTLSec, quotaTTLSec}
// 返回 {status, retryAfterSec}：status ∈ OK | COOLDOWN | TARGET_LIMIT | IP_LIMIT。
// 全部检查通过才写入，因此并发请求中至多一个 OK。
var issueScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
  return {'COOLDOWN', redis.call('TTL', KEYS[1])}
end
local t = tonumber(redis.call('GET', KEYS[2]) or '0')
if t >= tonumber(ARGV[2]) then
  return {'TARGET_LIMIT', redis.call('TTL', KEYS[2])}
end
local i = tonumber(redis.call('GET', KEYS[3]) or '0')
if i >= tonumber(ARGV[3]) then
  return {'IP_LIMIT', redis.call('TTL', KEYS[3])}
end
redis.call('SET', KEYS[1], '1', 'EX', ARGV[1])
if redis.call('INCR', KEYS[2]) == 1 then redis.call('EXPIRE', KEYS[2], ARGV[6]) end
if redis.call('INCR', KEYS[3]) == 1 then redis.call('EXPIRE', KEYS[3], ARGV[6]) end
redis.call('DEL', KEYS[4])
redis.call('HSET', KEYS[4], 'h', ARGV[4], 'n', '0')
redis.call('EXPIRE', KEYS[4], ARGV[5])
return {'OK', 0}
`)

// verifyScript：KEYS = {code}，ARGV = {maxAttempts, digest1, digest2, ...}
// 先计数、再在脚本内部完成摘要比对，比对与删除同在一次脚本执行内完成，避免并发校验之间
// 出现"都读到未删除的码、都判定匹配"的竞态（Go 侧再比对一次已经太晚）。
// 返回 {status, unused}：MISSING（键不存在）、EXHAUSTED（超过上限，键已删）、
// OK（匹配某个候选摘要，键已删）、MISMATCH（都不匹配，键保留供后续尝试）。
var verifyScript = redis.NewScript(`
local key = KEYS[1]
if redis.call('EXISTS', key) == 0 then
  return {'MISSING', ''}
end
local n = redis.call('HINCRBY', key, 'n', 1)
if n > tonumber(ARGV[1]) then
  redis.call('DEL', key)
  return {'EXHAUSTED', ''}
end
local stored = redis.call('HGET', key, 'h')
for i = 2, #ARGV do
  if stored == ARGV[i] then
    redis.call('DEL', key)
    return {'OK', ''}
  end
end
return {'MISMATCH', ''}
`)
