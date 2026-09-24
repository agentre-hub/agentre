package ctlcmd

// agentEnvMarkers 是已知 agent CLI 会放进它起的子进程环境里的标记：agrctl 用本机握手
// token、stdin 又是 TTY 时，光凭 TTY 判不出「人在敲键盘」还是「agent CLI 自己在终端里起了
// 一个子进程」——后者的子进程继承的 stdin 往往还连着同一个终端。带着这里任一标记就不算
// HUMAN，见 catalog.caller。
//
// 每一条都在本机装的对应 CLI 二进制或其官方源码里核实过（核实时间：2026-09-25）：
//   - AI_AGENT / CLAUDECODE / CLAUDE_CODE_ENTRYPOINT：Claude Code 2.1.280，Bash 工具起的
//     子进程里实测存在（curr session 的 env 现场核实，非文档推断）。
//   - GEMINI_CLI：@google/gemini-cli 0.33.1 依赖的 @google/gemini-cli-core 里，
//     shellExecutionService.js 给 shell 工具子进程的 env 写死注入
//     GEMINI_CLI_IDENTIFICATION_ENV_VAR="GEMINI_CLI" → GEMINI_CLI_IDENTIFICATION_ENV_VAR_VALUE="1"；
//     该文件顶部注释原话：「by downstream executables and scripts to identify that they
//     were executed from within Gemini CLI」。
//   - OPENCODE：本机 opencode 1.14.28 二进制里，启动时直接
//     process.env.AGENT="1"; process.env.OPENCODE="1"; process.env.OPENCODE_PID=...
//     写自己的进程环境（而不是只给某个工具子进程），因此子进程全部继承；这里只取
//     OPENCODE 这个专用名字，不取更容易撞车的通用 AGENT。
//   - CODEX_SANDBOX：本机 codex 0.156.0 二进制的 codex_core::spawn（core/src/spawn.rs）
//     给沙箱内跑的子进程注入 CODEX_SANDBOX=seatbelt（连同
//     CODEX_SANDBOX_NETWORK_DISABLED=1，禁网时）。已知局限：这标记只在 codex 的沙箱
//     （seatbelt / landlock）开着时才会出现，`--dangerously-bypass-approvals-and-sandbox`
//     等旁路场景不设置——查不到更通用的 codex 标记，如实记录在这里而不是假装覆盖了它。
var agentEnvMarkers = []string{
	"AI_AGENT",
	"CLAUDECODE",
	"CLAUDE_CODE_ENTRYPOINT",
	"GEMINI_CLI",
	"OPENCODE",
	"CODEX_SANDBOX",
}

// hasAgentEnvMarker 报告环境里是否带着 agentEnvMarkers 里任一已知 agent CLI 的标记。
func hasAgentEnvMarker(lookupEnv func(string) (string, bool)) bool {
	for _, marker := range agentEnvMarkers {
		if _, ok := lookupEnv(marker); ok {
			return true
		}
	}
	return false
}
