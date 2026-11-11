-- release_hold.lua: the reverse of hold_booking.lua. Give the stock back and delete the NIK locks, atomically.
--
-- KEYS  stock:{ev}:<cat> x ncat, then nik:used:{ev}:<hmac> x (the rest)
-- ARGV  ncat, qty_1 .. qty_ncat   (qty_i belongs to KEYS[i])
-- RET   {niks_deleted, stock_missing}
--         niks_deleted   how many NIK locks existed and were deleted
--         stock_missing  how many stock keys were gone and were therefore NOT recreated
-- A stock key that is gone stays gone: INCRBY would make up a count from nothing, and the next hold would sell seats
-- nobody counted instead of answering STOCK_UNINITIALIZED. Stock reconciliation (B5) restores such a key.
-- Every argument is checked before anything is written, so a bad call changes nothing.
-- Call it once per hold: it adds the stock back every time it runs. The callers (a failed booking transaction, the
-- HELD -> EXPIRED sweep after its commit) release a hold exactly once; a crash in between is repaired by stock
-- reconciliation, not by running this twice.
local ncat = tonumber(ARGV[1])
if ncat == nil or ncat < 1 or #ARGV ~= 1 + ncat or #KEYS < ncat then
  return redis.error_reply('release_hold: ARGV or KEYS do not match ncat')
end
local qty = {}
for i = 1, ncat do
  local q = tonumber(ARGV[1 + i])
  if q == nil or q < 1 or q ~= math.floor(q) then return redis.error_reply('release_hold: bad quantity') end
  qty[i] = q
end

local missing = 0
for i = 1, ncat do
  if redis.call('EXISTS', KEYS[i]) == 1 then
    redis.call('INCRBY', KEYS[i], qty[i])
  else
    missing = missing + 1
  end
end
local deleted = 0
for j = ncat + 1, #KEYS do
  deleted = deleted + redis.call('DEL', KEYS[j])
end
return {deleted, missing}
