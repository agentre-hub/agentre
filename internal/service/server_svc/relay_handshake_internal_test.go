package server_svc

import (
	"context"
	"errors"
	"io"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/relaytransport"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// closedWithErrorChannel 复现中继回通道级错误后的那一瞬:对端先写错误帧、再发空载荷
// 关通道,两帧都已经进了本端缓冲 —— 所以写已经失败(通道关了),读却还能拿到那帧错误。
type closedWithErrorChannel struct {
	pending [][]byte
	done    chan struct{}
}

func (c *closedWithErrorChannel) ReadPayload() ([]byte, error) {
	if len(c.pending) == 0 {
		return nil, io.EOF
	}
	payload := c.pending[0]
	c.pending = c.pending[1:]
	return payload, nil
}
func (c *closedWithErrorChannel) WritePayload([]byte) error { return relaytransport.ErrClosed }
func (c *closedWithErrorChannel) Close() error              { return nil }
func (c *closedWithErrorChannel) Done() <-chan struct{}     { return c.done }

func TestAuthAccountOverChannel_GivenTheRelayClosedTheChannelWithAnError_WhenTheRequestWriteLosesTheRace_ThenTheChannelErrorWins(t *testing.T) {
	errFrame, err := proto.Marshal(&agentrewire.RpcFrame{Body: &agentrewire.RpcFrame_Error{
		Error: &agentrewire.RpcError{Code: channelCodeTargetOffline, Message: "channel failed"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	channel := &closedWithErrorChannel{pending: [][]byte{errFrame}, done: done}

	_, err = authAccountOverChannel(context.Background(), channel, "credential")

	if !errors.Is(err, client.ErrRelayDaemonOffline) {
		t.Fatalf("通道已带着错误码关掉时必须报那个错误码, 实际拿到 %v", err)
	}
}
