-- advance_now_serving.lua (ADR-006 §3.4, §8.11, §8.12). Rules: features/advance_now_serving.feature.
--
-- KEYS[1]    = queue:{ev}            hash: state, issued, now_serving, full_booked_at, free_slots, free_slots_at
-- KEYS[2]    = queue:{ev}:by_ticket  sorted set: queue token -> ticket number
-- KEYS[3..n] = stock:{ev}:{category}
-- ARGV[1] MAX_CONCURRENT_SLOTS · ARGV[2] open (incl. pending) · ARGV[3] browsing (incl. pending)
-- ARGV[4] now_serving the controller read (CAS fence) · ARGV[5] current time (epoch seconds)
-- RET {first_admitted, last_admitted, result_code, admitted_tokens}
--
-- Tokens are never removed here: a by_ticket member with a ticket number <= now_serving is a pending
-- grant that admission-ctl finishes and ZREMs after its GRANTED row exists.

-- Redis does not roll back a half-run script, so every argument is checked before the first write.
local numbers = {}
for i = 1, 5 do
  local number = tonumber(ARGV[i])
  if number == nil or number ~= number or number < 0 or number > 9007199254740992
      or number ~= math.floor(number) then
    return redis.error_reply('invalid argument ARGV[' .. i .. ']: ' .. tostring(ARGV[i]))
  end
  numbers[i] = number
end
local max_concurrent_slots = numbers[1]
local open_count           = numbers[2]
local browsing_count       = numbers[3]
local now_serving_seen_by_controller = numbers[4]
local now_epoch_seconds    = numbers[5]

local QUEUE_HASH = KEYS[1]
local BY_TICKET  = KEYS[2]
local FIRST_STOCK_KEY = 3

-- Reads that can only yield a number: a missing or garbage field counts as 0.
local current_now_serving = tonumber(redis.call('HGET', QUEUE_HASH, 'now_serving') or 0) or 0
local last_issued_ticket  = tonumber(redis.call('HGET', QUEUE_HASH, 'issued') or 0) or 0

-- Another writer already moved the queue: do nothing.
if current_now_serving ~= now_serving_seen_by_controller then
  return {current_now_serving, current_now_serving, 'STALE', {}}
end

local event_state = redis.call('HGET', QUEUE_HASH, 'state')
if event_state ~= 'OPEN' and event_state ~= 'FULL_BOOKED' and event_state ~= 'SCHEDULED' then
  -- CLOSED, or the event was never seeded: touch nothing.
  return {current_now_serving, current_now_serving, event_state or 'MISSING', {}}
end

if #KEYS < FIRST_STOCK_KEY then
  return {current_now_serving, current_now_serving, 'STOCK_UNINITIALIZED', {}}
end
local total_stock = 0
for i = FIRST_STOCK_KEY, #KEYS do
  local category_stock = redis.call('GET', KEYS[i])
  if not category_stock then  -- a missing key is not zero
    return {current_now_serving, current_now_serving, 'STOCK_UNINITIALIZED', {}}
  end
  total_stock = total_stock + tonumber(category_stock)
end

-- Tells join how many more customers could be let in right now (ADR-006 §8.11).
local function record_free_slots(free_slots)
  redis.call('HSET', QUEUE_HASH,
    'free_slots', math.max(0, free_slots),
    'free_slots_at', now_epoch_seconds)
end

-- ADR-006 §3.3: limited by free places inside, and by a third of the stock minus people still browsing.
local places_left_inside = max_concurrent_slots - open_count
local places_left_by_stock = math.ceil(total_stock / 3) - browsing_count
local places_to_fill = math.min(places_left_inside, places_left_by_stock)

if event_state == 'SCHEDULED' then  -- before the sale opens: only record what would fit
  record_free_slots(total_stock > 0 and places_to_fill or 0)
  return {current_now_serving, current_now_serving, 'SCHEDULED', {}}
end

if total_stock <= 0 then
  if event_state == 'OPEN' then  -- temporary, not terminal: stock can come back
    redis.call('HSET', QUEUE_HASH, 'state', 'FULL_BOOKED', 'full_booked_at', now_epoch_seconds)
  end
  record_free_slots(0)
  return {current_now_serving, current_now_serving, 'FULL_BOOKED', {}}
end
if event_state == 'FULL_BOOKED' then  -- stock is back: reopen and admit in this same run
  redis.call('HSET', QUEUE_HASH, 'state', 'OPEN')
  redis.call('HDEL', QUEUE_HASH, 'full_booked_at')
end

if places_to_fill <= 0 then
  record_free_slots(0)
  return {current_now_serving, current_now_serving, 'NOOP', {}}
end

-- now_serving never passes the last ticket handed out.
local new_now_serving = math.min(current_now_serving + places_to_fill, last_issued_ticket)
if new_now_serving <= current_now_serving then
  record_free_slots(places_to_fill)
  return {current_now_serving, current_now_serving, 'EMPTY', {}}
end

local first_admitted_ticket = current_now_serving + 1
local admitted_tokens = redis.call('ZRANGEBYSCORE', BY_TICKET, first_admitted_ticket, new_now_serving)  -- read only, no ZREM
redis.call('HSET', QUEUE_HASH, 'now_serving', new_now_serving)

local tickets_admitted = new_now_serving - current_now_serving
record_free_slots(places_to_fill - tickets_admitted)
return {first_admitted_ticket, new_now_serving, 'ADMITTED', admitted_tokens}
