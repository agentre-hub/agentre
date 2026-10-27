package peer

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// 仅替换本测试消费的服务方法，其余端口不会被调用。
type cliModelChatPort interface {
	chat_svc.ChatSvc
	inboundSessionAdapter
}
type cliModelChat struct {
	cliModelChatPort
	requests chan *chat_svc.SetChatSessionCLIModelRequest
}

func (c *cliModelChat) SetChatSessionCLIModel(_ context.Context, req *chat_svc.SetChatSessionCLIModelRequest) (*chat_svc.SetChatSessionCLIModelResponse, error) {
	c.requests <- req
	return &chat_svc.SetChatSessionCLIModelResponse{}, nil
}

func TestProductionCLIModelComposition(t *testing.T) {
	convey.Convey("Given desktop production wiring, when setting or resetting by conversation UUID, then chat_svc receives the resolved local session and unchanged selection", t, func() {
		repo := mock_chat_repo.NewMockSessionRepo(gomock.NewController(t))
		oldRepo, oldChat := chat_repo.Session(), chat_svc.Chat()
		defer chat_repo.RegisterSession(oldRepo)
		defer chat_svc.RegisterChat(oldChat)
		chat_repo.RegisterSession(repo)
		svc := &cliModelChat{requests: make(chan *chat_svc.SetChatSessionCLIModelRequest, 1)}
		chat_svc.RegisterChat(svc)
		repo.EXPECT().FindByConversationID(gomock.Any(), convID(7)).Return(&chat_entity.Session{ID: 71, ConversationID: convID(7)}, nil).Times(2)
		deps := productionProtobufInboundDeps(nil)
		for _, choice := range [][2]string{{"fixed", "sonnet"}, {"", ""}} {
			require.NoError(t, deps.SetSessionCLIModel(context.Background(), convID(7), choice[0], choice[1]))
			require.Equal(t, &chat_svc.SetChatSessionCLIModelRequest{SessionID: 71, Mode: choice[0], Value: choice[1]}, <-svc.requests)
		}
	})
}

func TestProtobufSessionCLIModel(t *testing.T) {
	convey.Convey("Given a desktop inbound connection, when selecting or resetting CLI model, then authentication and raw errors are preserved", t, func() {
		calls := make(chan [3]string, 3)
		reg := NewProtobufInboundRegistry(ProtobufInboundDeps{SetSessionCLIModel: func(_ context.Context, id, mode, value string) error {
			calls <- [3]string{id, mode, value}
			if value == "bad" {
				return &protorpc.Error{Code: -32000, Message: "Model 'bad' not found"}
			}
			return nil
		}})
		a, b := peerProtoPipePair()
		client, server := protorpc.NewConn(a, protorpc.NewRegistry()), protorpc.NewConn(b, reg)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		defer func() { _ = client.Close() }()
		defer func() { _ = server.Close() }()
		go client.Serve(ctx)
		go server.Serve(ctx)
		_, err := wirecall.SetSessionCLIModel(ctx, wirecall.On(client), &agentrewire.SetSessionCLIModelRequest{ConversationId: convID(7)})
		var rpcErr *protorpc.Error
		require.ErrorAs(t, err, &rpcErr)
		require.EqualValues(t, -32001, rpcErr.Code)
		server.SetAuth(protorpc.AuthState{Authenticated: true})
		for _, choice := range [][2]string{{"fixed", "sonnet"}, {"", ""}, {"fixed", "bad"}} {
			_, err := wirecall.SetSessionCLIModel(ctx, wirecall.On(client), &agentrewire.SetSessionCLIModelRequest{ConversationId: convID(7), Mode: choice[0], Value: choice[1]})
			if choice[1] == "bad" {
				require.ErrorAs(t, err, &rpcErr)
				require.Equal(t, "Model 'bad' not found", rpcErr.Message)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, [3]string{convID(7), choice[0], choice[1]}, <-calls)
		}
	})
}
