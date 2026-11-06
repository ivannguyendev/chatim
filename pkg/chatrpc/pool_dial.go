package chatrpc

import (
	"io"

	"google.golang.org/grpc/credentials/insecure"

	"github.com/ivannguyendev/chatim/pkg/core/grpcclient"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type chatAPIs struct {
	chatimv1.ChatServiceClient
	chatimv1.MentionServiceClient
	chatimv1.MediaServiceClient
}

func DialInsecure(addr string) (chatimv1.ChatServiceClient, io.Closer, error) {
	cc, err := grpcclient.New(addr, grpcclient.Options{Creds: insecure.NewCredentials()})
	if err != nil {
		return nil, nil, err
	}
	return chatAPIs{
		ChatServiceClient:    chatimv1.NewChatServiceClient(cc),
		MentionServiceClient: chatimv1.NewMentionServiceClient(cc),
		MediaServiceClient:   chatimv1.NewMediaServiceClient(cc),
	}, cc, nil
}
