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
	repo.EXPECT().ListVersions(gomock.Any(), int64(7)).Return(versionsOf(msgs), listErr).AnyTimes()
	repo.EXPECT().FillBlocksByType(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(fillBlocksByTypeFrom(t, msgs)).AnyTimes()
}

// registerMessageMock 注入一个严格的 MessageRepo mock:没写期望的方法被调用即失败。
func registerMessageMock(t *testing.T) *mock_transcript_repo.MockMessageRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_transcript_repo.NewMockMessageRepo(ctrl)
	// 写过路径按会话缓存在包级;每个用例从空缓存起步,不吃上一个用例的 fixture。
	resetWrittenPathsCache()
	t.Cleanup(resetWrittenPathsCache)
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

// resetWrittenPathsCache 清空写过路径缓存。
func resetWrittenPathsCache() {
	writtenPathsCache.mu.Lock()
	defer writtenPathsCache.mu.Unlock()
	writtenPathsCache.bySession = nil
	writtenPathsCache.recent = nil
}

// versionsOf 模拟 ListVersions:同一批消息的 (id, 版本)。
func versionsOf(msgs []*chat_entity.Message) []transcript_repo.MessageVersion {
	out := make([]transcript_repo.MessageVersion, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, transcript_repo.MessageVersion{ID: m.ID, Version: m.Updatetime})
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
			// 严格 mock:List / ListMeta 没有期望,被调用即失败。
			repo := registerMessageMock(t)
			repo.EXPECT().ListVersions(gomock.Any(), int64(7)).Return(versionsOf(msgs), nil).Times(1)
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

// writeMsg 造一条带版本的写入消息:一个 Write 工具调用写 path。
func writeMsg(t *testing.T, id, version int64, path string) *chat_entity.Message {
	t.Helper()
	m := msgWithBlocks(t, id, blocks.ToolUseBlock{
		ID: "w", Name: "Write", Input: map[string]any{"file_path": path, "content": "x"},
	})
	m.Updatetime = version
	return m
}

// TestSessionWrittenPaths_IncrementalByVersion 钉住决策 4 的后半句:工作根的「写过的
// 路径」不再每轮全量读块。WorkRoots 每次轮末都要它,而会话的工具历史绝大多数自上一轮
// 起没变过 —— 只重读版本变了的那几条消息,删掉的消息直接从结果里消失。
func TestSessionWrittenPaths_IncrementalByVersion(t *testing.T) {
	convey.Convey("SessionWrittenPaths 按消息版本增量维护", t, func() {
		ctx := context.Background()
		repo := registerMessageMock(t)
		current := []*chat_entity.Message{writeMsg(t, 1, 100, "/wt/a.go"), writeMsg(t, 2, 200, "/wt/b.go")}
		repo.EXPECT().ListVersions(gomock.Any(), int64(7)).DoAndReturn(
			func(context.Context, int64) ([]transcript_repo.MessageVersion, error) {
				return versionsOf(current), nil
			}).AnyTimes()
		var refilled [][]int64
		repo.EXPECT().FillBlocksByType(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(c context.Context, msgs []*chat_entity.Message, types []string) error {
				ids := make([]int64, 0, len(msgs))
				for _, m := range msgs {
					ids = append(ids, m.ID)
				}
				refilled = append(refilled, ids)
				return fillBlocksByTypeFrom(t, current)(c, msgs, types)
			}).AnyTimes()
		// 严格 mock:ListMeta / List 没有期望 —— 增量维护不读整条会话的元数据。

		first, err := SessionWrittenPaths(ctx, 7)
		require.NoError(t, err)
		assert.Equal(t, []string{"/wt/a.go", "/wt/b.go"}, first)
		require.Len(t, refilled, 1)

		convey.Convey("转录没变 → 第二次不再读任何块", func() {
			again, err := SessionWrittenPaths(ctx, 7)
			require.NoError(t, err)
			assert.Equal(t, first, again)
			assert.Len(t, refilled, 1, "FillBlocksByType 只在第一次调用")
		})

		convey.Convey("新写入的消息 → 只读这一条,路径追加在后", func() {
			current = append(current, writeMsg(t, 3, 300, "/wt/c.go"))
			got, err := SessionWrittenPaths(ctx, 7)
			require.NoError(t, err)
			assert.Equal(t, []string{"/wt/a.go", "/wt/b.go", "/wt/c.go"}, got)
			assert.Equal(t, []int64{3}, refilled[len(refilled)-1])
		})

		convey.Convey("旧消息被改写(版本推进)→ 只重读它,旧路径换成新路径", func() {
			current[0] = writeMsg(t, 1, 400, "/wt/a2.go")
			got, err := SessionWrittenPaths(ctx, 7)
			require.NoError(t, err)
			assert.Equal(t, []string{"/wt/a2.go", "/wt/b.go"}, got)
			assert.Equal(t, []int64{1}, refilled[len(refilled)-1])
		})

		convey.Convey("消息被删(编辑重发 / 截断)→ 它的路径消失,不读块", func() {
			current = current[:1]
			got, err := SessionWrittenPaths(ctx, 7)
			require.NoError(t, err)
			assert.Equal(t, []string{"/wt/a.go"}, got)
			assert.Len(t, refilled, 1)
		})
	})
}
