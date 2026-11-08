package attach

import (
	"context"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

const cleanupFailedMsg = "media could not delete a moved object from processing; the retention rule removes it later"

func (s *Service) attach(ctx context.Context, tenant, messageKey string, keys []string) error {
	keys = unique(keys)
	uploads, err := s.uploadsOf(ctx, tenant, keys)
	if err != nil {
		return err
	}
	now := s.deps.Now().UTC()
	valid, drops, err := classify(tenant, keys, uploads, now)
	if err != nil {
		return err
	}
	if len(valid) == 0 {
		s.countDrops(drops)
		return nil
	}
	pairs := make([]domain.MessageFile, len(valid))
	fileKeys := make([]string, len(valid))
	for i, u := range valid {
		pairs[i] = domain.MessageFile{MessageKey: messageKey, FileKey: u.Key, Tenant: tenant, State: domain.PairLive, CreatedAt: now}
		fileKeys[i] = u.Key
	}
	if err := s.deps.Files.Attach(ctx, pairs); err != nil {
		return err
	}
	if err := s.deps.Uploads.Unretire(ctx, fileKeys); err != nil {
		return err
	}
	s.countDrops(drops)
	for _, u := range valid {
		if u.Bucket == domain.BucketCompleted {
			continue
		}
		if err := s.move(ctx, u, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) countDrops(drops []Reason) {
	for _, r := range drops {
		s.stats.Drop(r)
	}
}

func (s *Service) uploadsOf(ctx context.Context, tenant string, keys []string) (map[string]domain.Upload, error) {
	owned := make([]string, 0, len(keys))
	for _, k := range keys {
		if ownedBy(k, tenant) {
			owned = append(owned, k)
		}
	}
	if len(owned) == 0 {
		return nil, nil
	}
	return s.deps.Uploads.GetMany(ctx, tenant, owned)
}

func classify(tenant string, keys []string, uploads map[string]domain.Upload, now time.Time) ([]domain.Upload, []Reason, error) {
	var valid []domain.Upload
	var drops []Reason
	for _, k := range keys {
		u, found := uploads[k]
		switch {
		case !ownedBy(k, tenant):
			drops = append(drops, ReasonTenant)
		case !found:
			drops = append(drops, ReasonNoUpload)
		case u.State == domain.StateUploaded:
			valid = append(valid, u)
		case u.State != domain.StateUploading:
			drops = append(drops, ReasonAborted)
		case now.Sub(u.CreatedAt) > UploadExpiry:
			drops = append(drops, ReasonExpired)
		default:
			return nil, nil, ErrWaitUpload
		}
	}
	return valid, drops, nil
}

func (s *Service) move(ctx context.Context, u domain.Upload, now time.Time) error {
	thumb, copied, err := s.copyBoth(ctx, u)
	if err == nil {
		err = s.deps.Uploads.MarkMoved(ctx, u.Key, u.Ver, now)
	}
	if err != nil {
		s.stats.move(ResultError)
		return err
	}
	s.stats.move(ResultOK)
	s.cleanup(ctx, u.Key)
	if copied {
		s.cleanup(ctx, thumb)
	}
	return nil
}

func (s *Service) copyBoth(ctx context.Context, u domain.Upload) (string, bool, error) {
	if err := s.deps.Objects.Copy(ctx, u.Key, u.Size, u.ContentType); err != nil {
		return "", false, err
	}
	thumb, ok := s.cfg.Thumbnails.KeyFor(u.Key, u.ContentType)
	if !ok {
		return "", false, nil
	}
	obj, found, err := s.deps.Objects.Stat(ctx, domain.BucketProcessing, thumb)
	if err != nil || !found {
		return "", false, err
	}
	if err := s.deps.Objects.Copy(ctx, thumb, obj.Size, obj.ContentType); err != nil {
		return "", false, err
	}
	return thumb, true, nil
}

func (s *Service) cleanup(ctx context.Context, key string) {
	if err := s.deps.Objects.DeleteProcessing(ctx, key); err != nil {
		s.stats.move(ResultCleanupError)
		s.deps.Log.WarnContext(ctx, cleanupFailedMsg, "err", err)
	}
}

func ownedBy(key, tenant string) bool {
	prefix, _, ok := strings.Cut(key, "/")
	return ok && prefix == tenant
}

func unique(keys []string) []string {
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}
