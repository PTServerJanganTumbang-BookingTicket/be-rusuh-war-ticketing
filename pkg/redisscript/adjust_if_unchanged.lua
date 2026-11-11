-- adjust_if_unchanged.lua: correct one stock counter, but only if it still holds the value the caller read. Used by stock
-- reconciliation (docs/05-booking/04-stock-reconciliation.md §4.1): a second run with the same observed value, or a second
-- reconciler, therefore never corrects twice.
--
-- KEYS[1] = stock:{ev}:<cat>     ARGV[1] = observed (the value the caller read)     ARGV[2] = delta (whole number)
-- RET   {code, value}
--         ADJUSTED    value = the new count       (the key held `observed` and observed + delta >= 0)
--         CHANGED     value = the current count   (the key moved since it was read: nothing written)
--         MISSING     value = -1                  (the key is gone, or is not a plain integer: nothing written; the caller
--                                                  restores it with SET NX instead)
--         BELOW_ZERO  value = the current count   (observed + delta < 0: nothing written)
-- Writes only KEYS[1], and never creates it.
local observed = tonumber(ARGV[1])
local delta = tonumber(ARGV[2])
if observed == nil or delta == nil or observed ~= math.floor(observed) or delta ~= math.floor(delta) then
  return redis.error_reply('adjust_if_unchanged: observed and delta must be whole numbers')
end
local cur = redis.call('GET', KEYS[1])
-- a missing key is the Lua value false, not nil; only a plain integer counts (DECRBY/INCRBY refuse anything else)
if cur == false or not string.match(cur, '^%-?%d+$') then return {'MISSING', -1} end
cur = tonumber(cur)
if cur ~= observed then return {'CHANGED', cur} end
if observed + delta < 0 then return {'BELOW_ZERO', cur} end
return {'ADJUSTED', redis.call('INCRBY', KEYS[1], delta)}
