package chat_svc

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	chatblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
)

// msgWithBlocks 造一条持久化形态的消息:blocks 经注册表编码进 BlocksJSON,
// 与真实读回路径(Message.GetBlocks)走同一份解码。
func msgWithBlocks(t *testing.T, id int64, bs ...blocks.ContentBlock) *chat_entity.Message {
	t.Helper()
	m := &chat_entity.Message{ID: id, SessionID: 7, Role: "assistant"}
	require.NoError(t, m.SetBlocks(bs))
	return m
}

// stubMessages 把 transcript_repo.Message() 换成按 msgs 作答的 mock:整条读(List)与
// 窄读(ListMeta + FillBlocksByType)都由同一份 fixture 派生,读法换了结论也不该变。
func stubMessages(t *testing.T, msgs []*chat_entity.Message, listErr error) {
	t.Helper()
	repo := registerMessageMock(t)
	repo.EXPECT().List(gomock.Any(), int64(7)).Return(msgs, listErr).AnyTimes()
	repo.EXPECT().ListMeta(gomock.Any(), int64(7)).Return(metaOf(msgs), listErr).AnyTimes()
	repo.EXPECT().FillBlocksByType(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(fillBlocksByTypeFrom(t, msgs)).AnyTimes()
}

// registerMessageMock 注入一个严格的 MessageRepo mock:没写期望的方法被调用即失败。
func registerMessageMock(t *testing.T) *mock_transcript_repo.MockMessageRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_transcript_repo.NewMockMessageRepo(ctrl)
	prev := transcript_repo.Message()
	transcript_repo.RegisterMessage(repo)
	t.Cleanup(func() { transcript_repo.RegisterMessage(prev) })
	return repo
}

// metaOf 模拟 ListMeta:同一批消息的元数据副本,正文留空串(「没补过」)。
func metaOf(msgs []*chat_entity.Message) []*chat_entity.Message {
	out := make([]*chat_entity.Message, 0, len(msgs))
	for _, m := range msgs {
		meta := *m
		meta.BlocksJSON = ""
		out = append(out, &meta)
	}
	return out
}

// fillBlocksByTypeFrom 模拟 FillBlocksByType:按 ID 回到 fixture 取正文,只留下点名类型的块。
func fillBlocksByTypeFrom(
	t *testing.T, source []*chat_entity.Message,
) func(context.Context, []*chat_entity.Message, []string) error {
	return func(_ context.Context, msgs []*chat_entity.Message, types []string) error {
		for _, m := range msgs {
			var stored []blocks.StoredBlock
			for _, src := range source {
				if src.ID == m.ID {
					require.NoError(t, json.Unmarshal([]byte(src.BlocksJSON), &stored))
				}
			}
			kept := make([]blocks.StoredBlock, 0, len(stored))
			for _, b := range stored {
				if slices.Contains(types, b.Type) {
					kept = append(kept, b)
				}
			}
			buf, err := json.Marshal(kept)
			require.NoError(t, err)
			m.BlocksJSON = string(buf)
		}
		return nil
	}
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

		convey.Convey("只按 tool_use / nested_tool_use 两类块补正文,不读回整条转录", func() {
			msgs := []*chat_entity.Message{
				msgWithBlocks(t, 1,
					blocks.TextBlock{Text: "long answer"},
					blocks.ToolUseBlock{ID: "t1", Name: "Write", Input: map[string]any{"file_path": "/wt/a.go", "content": "x"}},
				),
				msgWithBlocks(t, 2, &chatblocks.NestedToolUseBlock{
					ID: "n1", Name: "Write", ParentToolCallID: "t0",
					Input: map[string]any{"file_path": "/wt/nested.go", "content": "x"},
				}),
			}
			// 严格 mock:List 没有期望,被调用即失败。
			repo := registerMessageMock(t)
			repo.EXPECT().ListMeta(gomock.Any(), int64(7)).Return(metaOf(msgs), nil).Times(1)
			repo.EXPECT().FillBlocksByType(gomock.Any(), gomock.Len(2),
				gomock.InAnyOrder([]string{"tool_use", "nested_tool_use"})).
				DoAndReturn(fillBlocksByTypeFrom(t, msgs)).Times(1)

			paths, err := SessionWrittenPaths(context.Background(), 7)
			require.NoError(t, err)
			assert.Equal(t, []string{"/wt/a.go", "/wt/nested.go"}, paths)
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
