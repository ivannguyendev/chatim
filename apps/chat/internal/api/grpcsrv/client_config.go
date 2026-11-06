package grpcsrv

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errNoClientConfig = fmt.Errorf("client config not wired: %w", apperr.ErrUnavailable)

func (s *Service) GetClientConfig(ctx context.Context, _ *chatimv1.GetClientConfigRequest) (*chatimv1.GetClientConfigResponse, error) {
	if _, err := chatrpc.CallerOf(ctx); err != nil {
		return nil, err
	}
	if s.clientConfig == nil {
		return nil, errNoClientConfig
	}
	return s.clientConfig(), nil
}
