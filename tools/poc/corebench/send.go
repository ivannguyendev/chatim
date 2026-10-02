package main

import (
	"context"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/status"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
	"github.com/ivannguyendev/chatim/tools/internal/route"
	"github.com/ivannguyendev/chatim/tools/poc/internal/msgtext"
	"github.com/ivannguyendev/chatim/tools/poc/internal/openloop"
)

const syntheticTexts = 4096

type room struct {
	id      string
	members []string
}

type bench struct {
	client  *route.Client
	tenant  string
	run     string
	texts   []string
	rooms   []room
	pick    *openloop.Picker
	stats   openloop.Stats
	live    *openloop.Live
	watched int
}

func (b *bench) fire(ctx context.Context, s openloop.Shot) {
	i := b.pick.Next()
	r := b.rooms[i]
	user := r.members[rand.IntN(len(r.members))]
	req := &chatimv1.SendMessageRequest{RoomId: r.id, Cid: openloop.CID(b.run, s.Index), Text: b.texts[rand.IntN(len(b.texts))]}
	_, st, err := b.client.SendMessage(route.WithCaller(ctx, b.tenant, user), req)
	b.stats.Record(openloop.Result{Shot: s, Done: time.Now(), Code: status.Code(err), Attempts: st.Attempts})
	if err == nil && s.Measured && i < b.watched {
		b.live.Expect()
	}
}

func loadTexts(path string) ([]string, error) {
	texts, err := msgtext.Load(path)
	if err != nil || len(texts) > 0 {
		return texts, err
	}
	rng := rand.New(rand.NewPCG(1, 2))
	texts = make([]string, syntheticTexts)
	for i := range texts {
		texts[i] = msgtext.Synthetic(rng)
	}
	return texts, nil
}

func slotShare(s *route.Session) string {
	owned := map[string]int{}
	for slot := range uint16(slotmap.Count) {
		rt, ok := s.Resolver.Slot(slot)
		switch {
		case !ok:
			owned["none"]++
		case rt.Owner:
			owned[rt.Core]++
		default:
			owned[rt.Core+"(preferred, no lease)"]++
		}
	}
	parts := make([]string, 0, len(owned))
	for _, k := range slices.Sorted(maps.Keys(owned)) {
		parts = append(parts, fmt.Sprintf("%s=%d", k, owned[k]))
	}
	return strings.Join(parts, " ")
}
