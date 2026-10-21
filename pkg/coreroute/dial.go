package coreroute

import (
	"context"
	"io"

	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/ivannguyendev/chatim/pkg/callerid"
	"github.com/ivannguyendev/chatim/pkg/grpcclient"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type coreAPIs struct {
	chatimv1.CoreServiceClient
	chatimv1.MentionServiceClient
}

func DialInsecure(addr string) (chatimv1.CoreServiceClient, io.Closer, error) {
	cc, err := grpcclient.New(addr, grpcclient.Options{Creds: insecure.NewCredentials()})
	if err != nil {
		return nil, nil, err
	}
	return coreAPIs{CoreServiceClient: chatimv1.NewCoreServiceClient(cc), MentionServiceClient: chatimv1.NewMentionServiceClient(cc)}, cc, nil
}

func WithCaller(ctx context.Context, tenant, user string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, callerid.TenantHeader, tenant, callerid.UserHeader, user)
}
