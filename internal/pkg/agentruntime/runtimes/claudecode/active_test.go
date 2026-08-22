package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
)

// TestBuildHookSettingsJSONString 钉死 hook 注册 JSON 的 schema:
// 顶层 hooks.PostToolUse[0].hooks[0].command 必须是
// "<bin> claudecode hook post-tool";含空格的路径要被 shellEscape 包成引号
// (claude CLI 内部用 sh -c 跑 hook command,不 escape 会 split 出错的 argv)。
func TestBuildHookSettingsJSONString(t *testing.T) {
	Convey("buildHookSettingsJSONString", t, func() {
		Convey("普通路径生成的 JSON 含 agentre claudecode hook post-tool", func() {
			out, err := buildHookSettingsJSONString("/usr/local/bin/agentre")
			So(err, ShouldBeNil)

			var parsed map[string]any
			So(json.Unmarshal([]byte(out), &parsed), ShouldBeNil)

			hooks, ok := parsed["hooks"].(map[string]any)
			So(ok, ShouldBeTrue)
			postToolUse, ok := hooks["PostToolUse"].([]any)
			So(ok, ShouldBeTrue)
			So(postToolUse, ShouldHaveLength, 1)

			entry := postToolUse[0].(map[string]any)
			inner := entry["hooks"].([]any)
			So(inner, ShouldHaveLength, 1)
			hook := inner[0].(map[string]any)
			So(hook["type"], ShouldEqual, "command")
			So(hook["command"], ShouldEqual, "/usr/local/bin/agentre claudecode hook post-tool")
		})

		Convey("路径含空格时 command 字段加引号 (避免 sh -c 时被 split)", func() {
			out, err := buildHookSettingsJSONString("/Applications/Agentre.app/Contents/MacOS/Agentre Helper")
			So(err, ShouldBeNil)
			// 解析后的 command 字段应当以引号包路径
			So(out, ShouldContainSubstring, `\"/Applications/Agentre.app/Contents/MacOS/Agentre Helper\" claudecode hook post-tool`)
		})
	})
}

// TestAcquireSession_InlineJSON_NoFilesWritten 验证 acquireSession 不再
// 落盘 settings.json,而是把 inline JSON 字符串透传给 ccLaunchSpec.Settings。
// 同时:AGENTRE_DATA_DIR 下不应出现 claudecode-runtime 目录。
func TestAcquireSession_InlineJSON_NoFilesWritten(t *testing.T) {
	Convey("Run 时", t, func() {
		dataDir := t.TempDir()
		t.Setenv("AGENTRE_DATA_DIR", dataDir)

		var (
			mu       sync.Mutex
			captured ccLaunchSpec
		)
		restore := SetSessionFactoryForTest(func(spec ccLaunchSpec) (ccSessionHandle, error) {
			mu.Lock()
			captured = spec
			mu.Unlock()
			return &fakeCCHandle{id: "fake-sid"}, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		events, _, err := r.Run(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			SessionID: 99,
			Cwd:       t.TempDir(),
			UserText:  "hi",
		})
		So(err, ShouldBeNil)
		for range events {
		}

		mu.Lock()
		settings := captured.Settings
		mu.Unlock()

		Convey("ccLaunchSpec.Settings 是 inline JSON,而非文件路径", func() {
			So(strings.HasPrefix(settings, "{"), ShouldBeTrue)
			So(json.Valid([]byte(settings)), ShouldBeTrue)
		})
		Convey("AGENTRE_DATA_DIR/claudecode-runtime 目录不应被创建", func() {
			runtimeRoot := filepath.Join(dataDir, "claudecode-runtime")
			_, statErr := os.Stat(runtimeRoot)
			So(os.IsNotExist(statErr), ShouldBeTrue)
		})

		r.CloseAllSessions(ctx)
	})
}

// Given claudeActive 是 CLISessionPool 里存的那个条目, When 优雅关闭超过宽限期、
// 池升级到硬杀, Then 这一刀必须真的落到 CLI 子进程上。
//
// 池的升级靠条目实现 Kill(ctx) error 来认领。claudeActive 从前只有 Close ——
// 而 claudecode 恰恰是唯一一个 Close 会永久阻塞的后端(关 stdin 后等一个根本不读
// stdin 的 CLI 退出),升级路径对它整个是空转。
func TestClaudeActive_GivenPoolEscalatesAfterGrace_WhenKilled_ThenSubprocessGetsTheSignal(t *testing.T) {
	Convey("claudeActive 作为池条目", t, func() {
		handle := &fakeCCHandle{killed: make(chan struct{})}
		active := &claudeActive{handle: handle}

		Convey("满足池的硬杀口契约", func() {
			killer, ok := any(active).(interface{ Kill(context.Context) error })
			So(ok, ShouldBeTrue)

			Convey("调用它把 SIGKILL 传到底层子进程", func() {
				So(killer.Kill(context.Background()), ShouldBeNil)
				So(atomic.LoadInt32(&handle.killCalls), ShouldEqual, 1)
			})
		})

		Convey("handle 缺席时不 panic", func() {
			bare, ok := any(&claudeActive{}).(interface{ Kill(context.Context) error })
			So(ok, ShouldBeTrue)
			So(bare.Kill(context.Background()), ShouldBeNil)
		})
	})
}
