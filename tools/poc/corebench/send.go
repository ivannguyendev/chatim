package main

import (
	"context"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"sync/atomic"
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

	mentionRate float64
	mentioned   atomic.Uint64
}

func (b *bench) fire(ctx context.Context, s openloop.Shot) {
	i := b.pick.Next()
	r := b.rooms[i]
	sender := rand.IntN(len(r.members))
	user := r.members[sender]
	req := &chatimv1.SendMessageRequest{RoomId: r.id, Cid: openloop.CID(b.run, s.Index), Text: b.texts[rand.IntN(len(b.texts))], Mentions: b.mentions(r, sender)}
	_, st, err := b.client.SendMessage(route.WithCaller(ctx, b.tenant, user), req)
	b.stats.Record(openloop.Result{Shot: s, Done: time.Now(), Code: status.Code(err), Attempts: st.Attempts})
	if err == nil && s.Measured && i < b.watched {
		b.live.Expect()
	}
}

func (b *bench) mentions(r room, sender int) *chatimv1.MentionSet {
	if b.mentionRate <= 0 || rand.Float64() >= b.mentionRate {
		return nil
	}
	if b.mentioned.Add(1)%20 == 0 {
		return &chatimv1.MentionSet{All: true}
	}
	target := sender
	if n := len(r.members); n > 1 {
		target = (sender + 1 + rand.IntN(n-1)) % n
	}
	return &chatimv1.MentionSet{Targets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: r.members[target]}}}
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
