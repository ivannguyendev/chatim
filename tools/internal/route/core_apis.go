package route

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errNoMentionService = status.Error(codes.Unimplemented, "route: the dialed core client has no mention service")

type coreAPIs struct {
	chatimv1.CoreServiceClient
	chatimv1.MentionServiceClient
}
