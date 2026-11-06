package upload

import (
	"context"
	"errors"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/objectstore"
)

func (s *Service) Complete(ctx context.Context, who Caller, key string, parts []domain.UploadedPart) (domain.Upload, error) {
	u, err := s.owned(ctx, who, key)
	if err != nil {
		return domain.Upload{}, err
	}
	switch u.State {
	case domain.StateUploaded:
		return u, nil
	case domain.StateAborted:
		return domain.Upload{}, domain.ErrNotUploading
	case domain.StateUploading:
	}
	sorted, err := domain.CheckUploadedParts(parts, u.PartCount)
	if err != nil {
		return domain.Upload{}, err
	}
	err = s.deps.Objects.Complete(ctx, u.Key, u.UploadID, sorted)
	if err != nil && !errors.Is(err, objectstore.ErrNoSuchUpload) {
		return domain.Upload{}, storageError(err)
	}
	return s.settle(ctx, u)
}

func (s *Service) settle(ctx context.Context, u domain.Upload) (domain.Upload, error) {
	size, err := s.deps.Objects.Head(ctx, u.Key)
	switch {
	case errors.Is(err, objectstore.ErrNotFound):
		return domain.Upload{}, domain.ErrUploadGone
	case err != nil:
		return domain.Upload{}, storageError(err)
	case size != u.Size:
		_ = s.deps.Objects.Delete(context.WithoutCancel(ctx), u.Key)
		_, _ = s.deps.Uploads.Transition(ctx, u.Key, domain.StateUploading, u.Ver, domain.StateAborted)
		return domain.Upload{}, domain.ErrSizeMismatch
	}
	done, err := s.deps.Uploads.Transition(ctx, u.Key, domain.StateUploading, u.Ver, domain.StateUploaded)
	if err == nil {
		s.completed.Add(1)
		return done, nil
	}
	return s.reread(ctx, u.Key, domain.StateUploaded, err)
}

func (s *Service) Abort(ctx context.Context, who Caller, key string) error {
	u, err := s.owned(ctx, who, key)
	if err != nil {
		return err
	}
	switch u.State {
	case domain.StateAborted:
		return nil
	case domain.StateUploaded:
		return domain.ErrNotUploading
	case domain.StateUploading:
	}
	err = s.deps.Objects.Abort(ctx, u.Key, u.UploadID)
	if errors.Is(err, objectstore.ErrNoSuchUpload) {
		_, headErr := s.deps.Objects.Head(ctx, u.Key)
		switch {
		case headErr == nil:
			return domain.ErrNotUploading
		case !errors.Is(headErr, objectstore.ErrNotFound):
			return storageError(headErr)
		}
	} else if err != nil {
		return storageError(err)
	}
	if _, err := s.deps.Uploads.Transition(ctx, u.Key, domain.StateUploading, u.Ver, domain.StateAborted); err != nil {
		_, err = s.reread(ctx, u.Key, domain.StateAborted, err)
		return err
	}
	return nil
}

func (s *Service) reread(ctx context.Context, key string, want domain.State, lost error) (domain.Upload, error) {
	now, err := s.deps.Uploads.Get(ctx, key)
	switch {
	case err != nil:
		return domain.Upload{}, err
	case now.State == want:
		return now, nil
	case now.State != domain.StateUploading:
		return domain.Upload{}, domain.ErrNotUploading
	}
	return domain.Upload{}, lost
}
