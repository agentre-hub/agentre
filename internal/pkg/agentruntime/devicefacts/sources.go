package devicefacts

import (
	"context"
	"sort"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	"github.com/agentre-hub/agentre/internal/pkg/clienv"
)

// DiscoverSkillPacks 用本机注册的技能包发现器列出某运行时已装的包 ID(排好序,内容
// 指纹因此不随发现器的列举顺序波动)。没有发现器的运行时没有包。两个宿主的 Sources
// 都接它。
func DiscoverSkillPacks(ctx context.Context, runtime, cliPath string) ([]string, error) {
	d, ok := agentskill.DiscovererFor(agent_backend_entity.BackendType(runtime))
	if !ok {
		return nil, nil
	}
	packs, err := d.Discover(ctx, agentskill.DiscoverQuery{CLIPath: cliPath})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(packs))
	for _, p := range packs {
		if p.ID != "" {
			ids = append(ids, p.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// ResolveCommand 判一条 ACP 命令在本机能否解析成一个可执行文件:带路径的看那个文件,
// 裸命令按运行时同一套增强 PATH 找(clienv,与 ACP 子进程启动同一口径)。
func ResolveCommand(command string) bool {
	_, ok := resolveExecutable(command)
	return ok
}

func resolveExecutable(command string) (string, bool) {
	path, ok := clienv.ResolveBinary(strings.TrimSpace(command))
	if !ok || !clienv.IsExecutableFile(path) {
		return "", false
	}
	return path, true
}

// CredentialSlot 是一份 Hermes / OpenClaw 引擎配置的凭据在本机的槽位名,与两个宿主
// 读写凭据时用的是同一个(backendcred):OpenClaw 按配置的 sync_id,Hermes 按规范化
// 后的 serve 地址。没有槽位(不是服务型引擎、没有 sync_id、地址坏了)时为空。
func CredentialSlot(e Engine) string {
	switch e.Runtime {
	case runtimeOpenClaw:
		return backendcred.OpenClawTokenAccount(e.SyncID)
	case runtimeHermes:
		base, err := agent_backend_entity.NormalizeHermesURL(e.HermesURL)
		if err != nil {
			return ""
		}
		return backendcred.HermesAccount(base)
	}
	return ""
}
