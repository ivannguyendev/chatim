package pbconv

import (
	"slices"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func Attachments(list []domain.Attachment) []*chatimv1.Attachment {
	if len(list) == 0 {
		return nil
	}
	out := make([]*chatimv1.Attachment, len(list))
	for i, a := range list {
		out[i] = &chatimv1.Attachment{Key: a.Key, Name: a.Name, Mime: a.Mime, Size: a.Size, Dim: dimOf(a.Dim), Dur: a.Dur, Hash: a.Hash}
	}
	return out
}

func DomainAttachments(list []*chatimv1.Attachment) []domain.Attachment {
	if len(list) == 0 {
		return nil
	}
	out := make([]domain.Attachment, len(list))
	for i, a := range list {
		out[i] = domain.Attachment{
			Key: a.GetKey(), Name: a.GetName(), Mime: a.GetMime(), Size: a.GetSize(), Dim: dimOf(a.GetDim()), Dur: a.GetDur(), Hash: a.GetHash(),
		}
	}
	return out
}

func dimOf(dim []int32) []int32 {
	if len(dim) == 0 {
		return nil
	}
	return slices.Clone(dim)
}
