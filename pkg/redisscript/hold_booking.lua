-- hold_booking.lua: take the seats of one order and lock every seat's NIK, all or nothing (ADR-005 §8).
--
-- KEYS  stock:{ev}:<cat> x ncat, then nik:used:{ev}:<hmac> x nnik   (one hash tag, one cluster slot)
-- ARGV  ncat, nnik, max_total, nik_ttl_seconds, qty_1 .. qty_ncat   (qty_i belongs to KEYS[i])
-- RET   {code, index}
--         HELD                  index -1
--         NO_STOCK              index = category (0-based) that is short; nothing changed
--         STOCK_UNINITIALIZED   index = category whose key is missing or unreadable; nothing changed
--         NIK_TAKEN             index = seat (0-based) whose NIK is already locked; the locks this call made are undone
--         STOCK_NEGATIVE        index = category that would go below zero; everything this call did is undone
--         QTY_INVALID           index = category with a quantity that is not a whole number >= 1
--         QTY_EXCEEDED          more seats than max_total
--         NIK_COUNT_MISMATCH    number of NIKs is not the number of seats
-- The three guards cannot happen for an order that passed validation (one seat = one ticket); they stop a caller bug
-- before it reaches stock. Writes only the keys it is given: never queue:{ev}.
local ncat = tonumber(ARGV[1])
local nnik = tonumber(ARGV[2])
if ncat == nil or nnik == nil or #KEYS ~= ncat + nnik then
  -- a caller bug: refuse before anything is written
  return redis.error_reply('hold_booking: KEYS do not match ncat + nnik')
end
local max_total = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])

local total = 0
local qty = {}
for i = 1, ncat do
  local q = tonumber(ARGV[4 + i])
  if q == nil or q < 1 or q ~= math.floor(q) then return {'QTY_INVALID', i - 1} end
  qty[i] = q
  total = total + q
end
if total > max_total then return {'QTY_EXCEEDED', -1} end
if total ~= nnik then return {'NIK_COUNT_MISMATCH', -1} end

-- Phase 1: look at every category before changing anything. A key that is gone is "unknown", never "sold out",
-- and it wins over a category that is merely short.
local have = {}
for i = 1, ncat do
  local v = redis.call('GET', KEYS[i])
  -- a missing key is the Lua value false, not nil. Only a plain integer counts: DECRBY refuses anything else, and by
  -- then the NIKs would be locked. "5.0", " 5", "0x5" and text are unreadable, never "enough".
  if v == false or not string.match(v, '^%-?%d+$') then return {'STOCK_UNINITIALIZED', i - 1} end
  have[i] = tonumber(v)
end
for i = 1, ncat do
  if have[i] < qty[i] then return {'NO_STOCK', i - 1} end
end

-- Phase 2: lock the NIKs. One already taken undoes the locks made so far.
local claimed = {}
for j = 1, nnik do
  local key = KEYS[ncat + j]
  if redis.call('SET', key, '1', 'NX', 'EX', ttl) then
    claimed[#claimed + 1] = key
  else
    for _, k in ipairs(claimed) do redis.call('DEL', k) end
    return {'NIK_TAKEN', j - 1}
  end
end

-- Phase 3: take the stock. A count below zero undoes everything (the same category listed twice, say).
local taken = {}
for i = 1, ncat do
  taken[#taken + 1] = i
  if redis.call('DECRBY', KEYS[i], qty[i]) < 0 then
    for _, t in ipairs(taken) do redis.call('INCRBY', KEYS[t], qty[t]) end
    for _, k in ipairs(claimed) do redis.call('DEL', k) end
    return {'STOCK_NEGATIVE', i - 1}
  end
end
return {'HELD', -1}
