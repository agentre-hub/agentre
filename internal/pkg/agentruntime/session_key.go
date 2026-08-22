package agentruntime

import (
	"strconv"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
)

// SessionPoolKey 交回某后端某条会话在 CLISessionPool 里的键。
//
// 键必须带后端命名空间:CLISessionPool 是进程级单例,claudecode 与 codex 的
// defaultRuntime 拿的是同一个实例。裸会话 id 会让两个后端上的同号会话在池里互相
// 顶掉 —— 后来的那条把前一条的子进程 Close 掉,而前一条还以为自己的会话活着。
//
// 键的形状由这一个地方决定,而不是各后端各写一份:约定散在各处时,新加的后端很容易
// 挑一个恰好撞上的形状,而撞上的表现是「另一个后端的会话莫名其妙断了」。
func SessionPoolKey(backend agent_backend_entity.BackendType, sessionID int64) string {
	return string(backend) + ":" + strconv.FormatInt(sessionID, 10)
}
