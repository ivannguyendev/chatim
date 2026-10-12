package mongostore

import "github.com/ivannguyendev/chatim/apps/core/internal/model/domain"

type editMentionDoc struct {
	Kind string `bson:"kind"`
	ID   string `bson:"id"`
}

func encodeEditMentions(targets []domain.MentionTarget) ([]editMentionDoc, error) {
	var out []editMentionDoc
	for _, t := range targets {
		name, err := mentionKindName(t.Kind)
		if err != nil {
			return nil, err
		}
		out = append(out, editMentionDoc{Kind: name, ID: t.ID})
	}
	return out, nil
}

func decodeEditMentions(docs []editMentionDoc) []domain.MentionTarget {
	var out []domain.MentionTarget
	for _, d := range docs {
		if kind, ok := mentionKindOf(d.Kind); ok {
			out = append(out, domain.MentionTarget{Kind: kind, ID: d.ID})
		}
	}
	return out
}
