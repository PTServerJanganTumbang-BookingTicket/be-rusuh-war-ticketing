-- hset_if_exists.lua: HSET fields on a hash only if the hash still exists.
-- A plain HSET on an expired hash would recreate it WITHOUT a TTL, and it would live forever.
-- KEYS[1] = the hash · ARGV = field, value, field, value ...
-- RET 1 when written, 0 when the hash does not exist.
if redis.call('EXISTS', KEYS[1]) == 0 then return 0 end
redis.call('HSET', KEYS[1], unpack(ARGV))
return 1
