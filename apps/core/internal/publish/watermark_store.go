package publish

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/redisguard"
)

const (
	watermarkPrefix = "chatim:pubwm:"
	activePrefix    = "chatim:active:"
	modeInit        = "init"
	modeMax         = "max"
)

var errWatermarksSkipped = errors.New("publish watermarks cooling down after a redis failure")

var syncScript = redis.NewScript(`
local function valid(v)
  if type(v) ~= 'string' then return false end
  if v == '0' then return true end
  if not string.match(v, '^[1-9]%d*$') then return false end
  return #v < 20 or (#v == 20 and v <= '18446744073709551615')
end
local function above(a, b)
  return #a > #b or (#a == #b and a > b)
end
local out = {}
for i, k in ipairs(KEYS) do
  local mode, want = ARGV[2 * i], ARGV[2 * i + 1]
  local cur = redis.pcall('GET', k)
  if not valid(cur) then cur = nil end
  if cur == nil or (mode == 'max' and above(want, cur)) then
    redis.call('SET', k, want, 'PX', ARGV[1])
    cur = want
  elseif mode == 'max' then
    redis.call('PEXPIRE', k, ARGV[1])
  end
  out[i] = cur
end
return out`)

func WatermarkKey(room uint64) string { return watermarkPrefix + strconv.FormatUint(room, 10) }

func ActiveKey(slot uint16) string { return activePrefix + strconv.Itoa(int(slot)) }

type syncReq struct {
	room  uint64
	init  bool
	value uint64
}

type watermarkStore struct {
	rdb   *redis.Client
	guard *redisguard.Guard
	ttl   time.Duration
}

func (w *watermarkStore) sync(ctx context.Context, reqs []syncReq) ([]uint64, error) {
	keys := make([]string, len(reqs))
	args := make([]any, 1, 1+2*len(reqs))
	args[0] = w.ttl.Milliseconds()
	for i, r := range reqs {
		keys[i] = WatermarkKey(r.room)
		mode := modeMax
		if r.init {
			mode = modeInit
		}
		args = append(args, mode, strconv.FormatUint(r.value, 10))
	}
	out := make([]uint64, len(reqs))
	err := w.guard.Do(ctx, "sync", func(cctx context.Context) error {
		raw, err := syncScript.Run(cctx, w.rdb, keys, args...).StringSlice()
		if err != nil {
			return err
		}
		if len(raw) != len(reqs) {
			return fmt.Errorf("watermark sync answered %d of %d rooms", len(raw), len(reqs))
		}
		for i, v := range raw {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return fmt.Errorf("watermark of room %d: %w", reqs[i].room, err)
			}
			out[i] = n
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
