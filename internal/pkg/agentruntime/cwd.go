package agentruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/paths"
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

// safeAgentSyncID 限定同步标识的**词表**。标识是从对端(浏览器 / 别的设备)原样收来的
// 字符串,不加约束地拼进路径,一个 "../.." 就能把 AppDataDir 之外的目录拖进来当 Agent
// 工作区。首字符要求是字母或数字,顺带把 "." 与 ".." 挡在门外;路径分隔符、空白与控制
// 字符一律不在词表里,所以过了这一关的标识不可能表达「上一级」或「另一个目录」。
//
// 冒号是 2026-09-18 加进来的:同步标识只有两种来源 —— 普通 Agent 的随机 ULID
// (syncmeta_entity.NewSyncID,[0-9A-Z]),和系统 Agent 那个固定值
// agent_entity.DefaultAgentSyncID = "agent:system:default-ceo"。后者带冒号,于是控制台
// 对系统 Agent 发起「不指定项目」的自由对话(AgentID=0 + Cwd 空)时恒定解不出目录。
// 冒号不表达层级,进不了词表的分隔符它一个也替代不了,放进来不削弱上面那条保证。
var safeAgentSyncID = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z_:-]{0,63}$`)

// pathLiteralAgentSyncIDChar 报告这个字符能不能原样出现在目录名里。
//
// 词表比「能当目录名的字符」宽一点,两者的差额由 escapeAgentSyncIDForPath 抹平。
func pathLiteralAgentSyncIDChar(c byte) bool {
	switch {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		return true
	case c == '_', c == '-':
		return true
	default:
		return false
	}
}

// escapeAgentSyncIDForPath 把词表内的标识翻成一个目录名:能原样落地的字符逐字保留,
// 其余转义成 "~" + 两位十六进制。今天唯一会被转义的是冒号("agent:system:default-ceo"
// → "agent~3Asystem~3Adefault-ceo")。
//
// 为什么不直接把冒号放进目录名(即只放宽正则、不做转义):Windows 上冒号是盘符与
// NTFS 备用数据流的分隔符,"<AppDataDir>/agents/sync-agent:system:default-ceo" 在那里
// 根本建不出来 —— 桌面端是跨平台的,只放宽词表等于把这个 bug 从「解析报错」搬成
// 「只在 Windows 上建目录失败」。POSIX 上冒号合法,但让同一个 Agent 在两个平台落到
// 不同目录也没有好处。
//
// 为什么不整串取哈希:ULID 那一类标识必须与改动前**逐字相同**,否则已有 Agent 的工作
// 目录会搬家、累积的用户文件凭空消失;哈希还会让 agents/ 下面变得不可读。转义在
// 词表原有的字符上是恒等映射,天然满足这条不变量,只有新放进词表的字符才改样子。
//
// 映射是单射:"~" 不在词表里,所以它只可能是转义标记,两个不同标识不会撞进同一个
// 目录。日后若把 "~" 加进词表,必须同时把它自己也转义,否则这条就不成立了。
func escapeAgentSyncIDForPath(id string) string {
	var b strings.Builder
	b.Grow(len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		if pathLiteralAgentSyncIDChar(c) {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "~%02X", c)
	}
	return b.String()
}

// ResolveAgentCwd 解析一轮执行的兜底工作目录 —— 各 runtime 在 RunRequest.Cwd 为空时
// 走这里,而不是直接调 AgentCwd。
//
//	agentID > 0 → AgentCwd(agentID),即 <AppDataDir>/agents/<agentID>/
//	agentID = 0 → <AppDataDir>/agents/sync-<escapeAgentSyncIDForPath(agentSyncID)>/
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
	dir := filepath.Join(root, "agents", "sync-"+escapeAgentSyncIDForPath(id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
