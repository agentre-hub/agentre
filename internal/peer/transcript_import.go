package peer

import (
	"context"
	"errors"
	"strings"

	"github.com/cago-frame/cago/pkg/utils/httputils"

	daemonimport "github.com/agentre-hub/agentre/internal/daemon/transcriptimport"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	importwire "github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
	"github.com/agentre-hub/agentre/internal/service/chat_import_svc"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// desktopTranscriptImport 是桌面端交给共用入站注册面的 transcriptimport 端口。
//
// 它必须存在:控制台的「导入历史会话」把 kind=desktop 的机器与 agentred 并排列出
// (useMachineReachability.tsx),server 后端拿到指纹就直接拨,全链路不筛 kind ——
// 桌面端不挂这一族,用户按下的每一次导入都只会撞 method not found。
//
// **只读的三个方法直接复用 daemon 那一份 handler。** 读取器来自
// internal/pkg/transcriptimport 的全局注册表,而桌面端进程里那张表本来就是满的
// (本机导入 chat_import_svc 在 deviceID == 0 时读的正是它);daemon 那份 handler 的
// Sessions / Transcript 等存储口只有 Execute 用得到,只读三个方法用不到。方言知识
// 因此只有一份,两个宿主答的话不会分叉。
//
// **Execute 不能同样复用。** daemon 那条路把会话落成 agentred 形状的身份行 + 通知
// 日志;而导进桌面端的会话导完之后要能在这台机器上接着聊,它必须与桌面端自己建的
// 会话长得一模一样(chat_sessions + agent / project 关联)。分叉点只在「写在哪」:
// 读取器与逐轮回放两端仍然是同一份代码,这里也不绕过 chat_import_svc 自己写一遍
// 导入 —— 那就成了第二条写库路径。
type desktopTranscriptImport struct {
	// readers 只用它的 Scan / Open / Turns。
	readers *daemonimport.Handlers
	// imports 懒解析导入服务,兼容 bootstrap 接线晚于注册面构造的时序
	// (同 protobuf_composition.go 里 chat_svc 的 adapter)。
	imports func() chat_import_svc.ChatImportSvc
}

func newDesktopTranscriptImport() *desktopTranscriptImport {
	return &desktopTranscriptImport{
		readers: daemonimport.NewHandlers(daemonimport.Options{}),
		imports: chat_import_svc.Default,
	}
}

func (d *desktopTranscriptImport) Scan(ctx context.Context, params importwire.ScanParams) (*importwire.ScanResult, error) {
	return d.readers.Scan(ctx, params)
}

func (d *desktopTranscriptImport) Open(ctx context.Context, params importwire.OpenParams) (*importwire.OpenResult, error) {
	return d.readers.Open(ctx, params)
}

func (d *desktopTranscriptImport) Turns(ctx context.Context, params importwire.TurnsParams) (*importwire.TurnsResult, error) {
	return d.readers.Turns(ctx, params)
}

// Execute 把这台机器磁盘上的一份转录落成一条**桌面端形状**的会话。
//
// 三格入参是跨机才有的,逐一说清:
//   - ConversationID 由调用方铸,本机原样用(发起端铸号、对端从不发号,与
//     runtime.run 的 RunFresh 同一条规矩)。
//   - AgentSyncID 是认 Agent 的唯一依据。请求里同时带着的 AgentID 是历史兼容的
//     本地主键,跨机一律不采信 —— 这里干脆不往下传。
//   - PeerFingerprint 语义同 runtime.run:省略 = 调用方自己的对端。桌面端只认得
//     自己这一个 origin(导进来的会话就落在这台机器上),点名别人一律拒。
//
// ProjectID 不传(自由会话):wire 上没有这一格,桌面端也没有第二个真相源能替用户
// 决定挂哪个项目。Cwd 同样留空 —— 那正是「用磁盘转录里记的 cwd」的语义,续跑要在
// 原目录起 CLI,不是在项目目录。
func (d *desktopTranscriptImport) Execute(ctx context.Context, params importwire.ExecuteParams) (*importwire.ExecuteResult, error) {
	if err := conversationid.Validate(params.ConversationID); err != nil {
		return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "invalid conversation id"}
	}
	if err := requireOwnOrigin(params.PeerFingerprint); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.AgentSyncID) == "" {
		// 缺标识时不退回 params.AgentID:那个号是发起端库里的自增主键,采信它会把
		// 会话静默落到本机那个碰巧同号的 Agent 名下(6425ba59 / 18b6b9c8)。
		return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "agent sync id required"}
	}
	out, err := d.imports().Import(ctx, &chat_import_svc.ImportRequest{
		Backend:        params.Backend,
		Locator:        params.Locator,
		ConversationID: params.ConversationID,
		AgentSyncID:    params.AgentSyncID,
	}, nil)
	if err != nil {
		return nil, importExecuteError(err)
	}
	if out.AlreadyImported {
		// 交回的是**库里那条**的身份,未必等于调用方刚铸的号(wire.ExecuteResult
		// 的契约):这份转录早就导过一次,收敛到已有的那条对话上,Turns 为 0。
		return &importwire.ExecuteResult{
			ConversationID: out.ConversationID, ProviderSessionID: out.ProviderSessionID,
			Cwd: out.Cwd, Title: out.Title, AlreadyImported: true,
		}, nil
	}
	return &importwire.ExecuteResult{
		ConversationID: out.ConversationID, ProviderSessionID: out.ProviderSessionID,
		Cwd: out.Cwd, Title: out.Title, Turns: out.ImportedTurns,
	}, nil
}

// importExecuteError 把导入服务的失败翻成这一族**既有**的线上词汇。
//
// 不翻的话每一种失败都是 -32603:「这台机器上没装那个 CLI」「转录文件没了」与
// 「本机出错了」在控制台上会长成同一句话,而前两条各有各的出路。桌面端因此和
// agentred 说同一种话 —— 调用方不必先问对面是哪一种执行端。
func importExecuteError(err error) error {
	if errors.Is(err, chat_import_svc.ErrAgentNotFound) {
		// 调用方点名的 Agent 在这台机器上不存在:是它的参数在这里不成立,
		// 不是本机出了故障。
		return &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: err.Error()}
	}
	var httpErr *httputils.Error
	if errors.As(err, &httpErr) {
		switch httpErr.Code {
		case code.ChatImportBackendUnavailable:
			return importwire.ErrBackendUnavailable
		case code.ChatImportTranscriptOpenFailed:
			return importwire.ErrTranscriptOpen
		case code.InvalidParameter:
			return &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: httpErr.Msg}
		}
	}
	return err
}
