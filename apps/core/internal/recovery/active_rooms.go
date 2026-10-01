package recovery

import (
	"context"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
)

var removeScript = redis.NewScript(`
local n = 0
for i = 1, #ARGV, 2 do
  local s = redis.call('ZSCORE', KEYS[1], ARGV[i])
  if s and tonumber(s) == tonumber(ARGV[i + 1]) then
    n = n + redis.call('ZREM', KEYS[1], ARGV[i])
  end
end
return n`)

type activeRoom struct {
	id     uint64
	member string
	score  float64
	wm     uint64
}

func (s *Sweeper) readActive(ctx context.Context, slot uint16, offset int64) ([]activeRoom, int, error) {
	zctx, cancel := context.WithTimeout(ctx, s.cfg.RedisTimeout)
	defer cancel()
	zs, err := s.deps.Redis.ZRangeWithScores(zctx, publish.ActiveKey(slot), offset, offset+int64(s.cfg.Batch)-1).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("read active rooms of slot %d: %w", slot, err)
	}
	rooms := make([]activeRoom, 0, len(zs))
	keys := make([]string, 0, len(zs))
	for _, z := range zs {
		member, _ := z.Member.(string)
		id, err := strconv.ParseUint(member, 10, 64)
		if err != nil || id == 0 {
			continue
		}
		rooms = append(rooms, activeRoom{id: id, member: member, score: z.Score})
		keys = append(keys, publish.WatermarkKey(id))
	}
	if len(keys) == 0 {
		return rooms, len(zs), nil
	}
	mctx, cancel := context.WithTimeout(ctx, s.cfg.RedisTimeout)
	defer cancel()
	values, err := s.deps.Redis.MGet(mctx, keys...).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("read publish watermarks of slot %d: %w", slot, err)
	}
	if len(values) != len(rooms) {
		return nil, 0, fmt.Errorf("read publish watermarks of slot %d: got %d of %d", slot, len(values), len(rooms))
	}
	for i, v := range values {
		rooms[i].wm = parseWatermark(v)
	}
	return rooms, len(zs), nil
}

func parseWatermark(v any) uint64 {
	text, ok := v.(string)
	if !ok {
		return 0
	}
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func (s *Sweeper) removeStale(ctx context.Context, slot uint16, rooms []activeRoom) (int, error) {
	removed := 0
	for len(rooms) > 0 {
		n := min(len(rooms), removeChunk)
		args := make([]any, 0, 2*n)
		for _, r := range rooms[:n] {
			args = append(args, r.member, strconv.FormatFloat(r.score, 'f', -1, 64))
		}
		rctx, cancel := context.WithTimeout(ctx, s.cfg.RedisTimeout)
		got, err := removeScript.Run(rctx, s.deps.Redis, []string{publish.ActiveKey(slot)}, args...).Int()
		cancel()
		if err != nil {
			return removed, fmt.Errorf("remove caught-up rooms of slot %d: %w", slot, err)
		}
		removed += got
		rooms = rooms[n:]
	}
	return removed, nil
}
