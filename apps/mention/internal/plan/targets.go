package plan

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var targetKinds = map[chatimv1.MentionKind]domain.TargetKind{
	chatimv1.MentionKind_MENTION_KIND_USER:  domain.TargetUser,
	chatimv1.MentionKind_MENTION_KIND_GROUP: domain.TargetGroup,
}

func targetsOf(in []*chatimv1.MentionTarget, everyone bool, everyoneAt *timestamppb.Timestamp, sent time.Time) ([]domain.Target, error) {
	if len(in) == 0 && !everyone {
		return nil, nil
	}
	out := make([]domain.Target, 0, len(in)+1)
	for _, t := range in {
		kind, ok := targetKinds[t.GetKind()]
		if !ok {
			return nil, invalid("mention kind")
		}
		out = append(out, domain.Target{Kind: kind, ID: t.GetId(), At: timeOr(t.GetAt(), sent)})
	}
	if everyone {
		out = append(out, domain.Target{Kind: domain.TargetAll, At: timeOr(everyoneAt, sent)})
	}
	return out, nil
}

func timeOr(t *timestamppb.Timestamp, fallback time.Time) time.Time {
	if valid(t) {
		return t.AsTime()
	}
	return fallback
}

func retired(previous, current []domain.Target) []domain.Target {
	kept := make(map[domain.Target]struct{}, len(current))
	for _, t := range current {
		kept[t.Identity()] = struct{}{}
	}
	var out []domain.Target
	for _, t := range previous {
		if _, ok := kept[t.Identity()]; !ok {
			out = append(out, t)
		}
	}
	return out
}
