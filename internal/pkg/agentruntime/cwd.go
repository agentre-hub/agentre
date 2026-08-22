package agentruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/agentre-ai/agentre/internal/pkg/paths"
)

// AgentCwd 给需要文件系统工具的后端拼一个稳定的 Agent 工作目录：
//
//	<AppDataDir>/agents/<agentID>/
//
// 同一 Agent 的所有聊天会话复用同一目录，便于内置工具和 CLI 后端累积用户文件。
// 会话软删除不清理该目录；它是 Agent 级工作区。
func AgentCwd(agentID int64) (string, error) {
	if agentID <= 0 {
		return "", fmt.Errorf("agentruntime: AgentCwd needs agentID > 0")
	}
	root, err := paths.AppDataDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "agents", fmt.Sprintf("%d", agentID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// safeAgentSyncID 限定同步标识能安全地当一个路径段用。标识是从对端(浏览器 / 别的
// 设备)原样收来的字符串,不加约束地拼进路径,一个 "../.." 就能把 AppDataDir 之外的
// 目录拖进来当 Agent 工作区。首字符要求是字母或数字,顺带把 "." 与 ".." 挡在门外。
var safeAgentSyncID = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z_-]{0,63}$`)

// ResolveAgentCwd 解析一轮执行的兜底工作目录 —— 各 runtime 在 RunRequest.Cwd 为空时
// 走这里,而不是直接调 AgentCwd。
//
//	agentID > 0 → AgentCwd(agentID),即 <AppDataDir>/agents/<agentID>/
//	agentID = 0 → <AppDataDir>/agents/sync-<agentSyncID>/
//
// 两条分支的差别只在「拿什么当 Agent 的身份」:桌面端进程里有本地自增主键就用它
// (老目录不搬家);从 web 发起的对话没有 —— 浏览器手里没有、也不该编一个桌面端本地
// 主键(见 RunRequest.AgentSyncID 与前端 dispatch.ts 里显式的 agentId: 0),身份只由
// 账号级同步标识表达。目录仍是 Agent 级:同一 Agent 的多条自由会话复用同一个。
//
// 两者都拿不出来时如实报错,不静默落到某个共用目录 —— 那会让两个 Agent 的文件混在一起。
func ResolveAgentCwd(agentID int64, agentSyncID string) (string, error) {
	if agentID > 0 {
		return AgentCwd(agentID)
	}
	id := strings.TrimSpace(agentSyncID)
	if !safeAgentSyncID.MatchString(id) {
		return "", fmt.Errorf(
			"agentruntime: ResolveAgentCwd needs agentID > 0 or a syntactically valid agentSyncID")
	}
	root, err := paths.AppDataDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "agents", "sync-"+id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
