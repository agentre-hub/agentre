package chat_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
)

// msgWithBlocks 造一条持久化形态的消息:blocks 经注册表编码进 BlocksJSON,
// 与真实读回路径(Message.GetBlocks)走同一份解码。
func msgWithBlocks(t *testing.T, id int64, bs ...blocks.ContentBlock) *chat_entity.Message {
	t.Helper()
	m := &chat_entity.Message{ID: id, SessionID: 7, Role: "assistant"}
	require.NoError(t, m.SetBlocks(bs))
	return m
}

// stubMessages 把 chat_repo.Message() 换成只会 List 出 msgs 的 mock。
func stubMessages(t *testing.T, msgs []*chat_entity.Message, listErr error) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_chat_repo.NewMockMessageRepo(ctrl)
	repo.EXPECT().List(gomock.Any(), int64(7)).Return(msgs, listErr).AnyTimes()
	prev := chat_repo.Message()
	chat_repo.RegisterMessage(repo)
	t.Cleanup(func() { chat_repo.RegisterMessage(prev) })
}

func TestSessionWrittenPaths(t *testing.T) {
	convey.Convey("SessionWrittenPaths 只收本会话 AI 写过的文件路径", t, func() {
		convey.Convey("Edit / Write 工具调用的路径按出现顺序去重返回", func() {
			stubMessages(t, []*chat_entity.Message{
				msgWithBlocks(t, 1, blocks.ToolUseBlock{
					ID: "t1", Name: "Write",
					Input: map[string]any{"file_path": "/wt/a.go", "content": "package a\n"},
				}),
				msgWithBlocks(t, 2, blocks.ToolUseBlock{
					ID: "t2", Name: "Edit",
					Input: map[string]any{
						"file_path": "/wt/b.go", "old_string": "x", "new_string": "y",
					},
				}),
				// 同一个文件写第二次:不重复出现。
				msgWithBlocks(t, 3, blocks.ToolUseBlock{
					ID: "t3", Name: "Write",
					Input: map[string]any{"file_path": "/wt/a.go", "content": "package a2\n"},
				}),
			}, nil)

			paths, err := SessionWrittenPaths(context.Background(), 7)
			require.NoError(t, err)
			assert.Equal(t, []string{"/wt/a.go", "/wt/b.go"}, paths)
		})

		convey.Convey("subagent 的嵌套工具调用同样计入(它也是 AI 的写入)", func() {
			stubMessages(t, []*chat_entity.Message{
				msgWithBlocks(t, 1, &chatblocks.NestedToolUseBlock{
					ID: "n1", Name: "Write", ParentToolCallID: "t0",
					Input: map[string]any{"file_path": "/wt/nested.go", "content": "x"},
				}),
			}, nil)

			paths, err := SessionWrittenPaths(context.Background(), 7)
			require.NoError(t, err)
			assert.Equal(t, []string{"/wt/nested.go"}, paths)
		})

		convey.Convey("只读工具与文本块不产生任何路径", func() {
			stubMessages(t, []*chat_entity.Message{
				msgWithBlocks(t, 1,
					blocks.TextBlock{Text: "/wt/not-a-write.go"},
					blocks.ToolUseBlock{ID: "t1", Name: "Read", Input: map[string]any{"file_path": "/wt/r.go"}},
					blocks.ToolUseBlock{ID: "t2", Name: "Bash", Input: map[string]any{"command": "rm /wt/x"}},
				),
			}, nil)

			paths, err := SessionWrittenPaths(context.Background(), 7)
			require.NoError(t, err)
			assert.Empty(t, paths)
		})

		convey.Convey("仓储报错原样冒泡,不静默降级成空清单", func() {
			stubMessages(t, nil, errors.New("db down"))

			_, err := SessionWrittenPaths(context.Background(), 7)
			require.Error(t, err)
		})

		convey.Convey("sessionID 非法 → 参数错误,不读库", func() {
			_, err := SessionWrittenPaths(context.Background(), 0)
			require.Error(t, err)
		})
	})
}
