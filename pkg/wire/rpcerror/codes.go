package rpcerror

// 领域错误码 —— 本包是它们的唯一主人。
//
// 这些数字是**过线的稳定协议值**:对端按码分支,不按 message 文本。它们此前住在
// 桌面仓 internal/ 的三个 wire 包里,共享 module 看不见,消费方(agentre-server)
// 只能手抄一份魔数 —— 改了这边,那边不会有任何地方变红。搬进来之后两侧同源。
//
// # 段位划分
//
// 同一条连接上跑着好几个方法族,而客户端只拿得到一个数字。两个族用同一个码,
// 客户端就会把别人的失败 rehydrate 成自己的 sentinel,所以每个族占一段互不重叠
// 的连续码:
//
//	-32001..-32006   daemon 会话 / 鉴权(见 error.go)
//	-32010..-32015   runtime.*
//	-32030..-32035   remotefs.*
//	-32040..-32043   workspacefs.*
//	-32050..-32052   project.*
//	-32060..-32062   transcriptImport.*
//	-32600..-32700   JSON-RPC 标准码(见 error.go)
//	-32800           取消(见 error.go)
//
// 加新码之前先给它的方法族划一段:segments_test.go 的撞号守卫会 AST 扫本包的
// 全部 Code* 常量,既不许两个名字共用一个数字,也不许有码落在所有段位之外。
// 那张段表与这段注释是同一件事的两种写法,守卫只认前者。
//
// 守卫只看得见**住在本包的**码,所以还没搬进来的族仍是盲区。transcriptImport.*
// 曾经就是这样一处:它在桌面仓自己声明 -32050..-32052,与 project.* 逐个撞上,
// 而两边都没有编译器或守卫看得见。搬进来的第一次运行守卫立刻判红,那一段因此
// 重新分到 -32060 —— 这正是这张表存在的理由。还没搬进来的族仍然是盲区。
//
// 与 error.go 里那几个 int32 常量不同,这一族是**无类型**常量:它们既要填进
// Error.Code(int32),也要填进线上帧里 int 的 stopErrCode 那一格,还要被各方法族
// 包起短名字用。定死一个类型只会逼着调用点写一圈转换,而转换写多了迟早有人写错
// 方向 —— 无类型常量让编译器在每个用处自己挑对的那个。
const (
	// ── runtime.* ──────────────────────────────────────────────────────────
	//
	// 前五个是 agentruntime 标准 sentinel 的稳定 wire 值,daemon 与桌面端靠
	// 它们让 errors.Is(err, agentruntime.ErrXxx) 跨机继续成立。

	CodeRuntimeNoActiveTurn  = -32010
	CodeRuntimeSteerNotFound = -32011
	CodeRuntimeUnsupported   = -32012
	// CodeRuntimeAborted 是「用户自己按了停止」。turnstate.AbortedCode 是同一个
	// 数字的另一处声明 —— 那个包判定一轮是否故障收场时不该反向依赖本包,两者由
	// segments_test.go 里的一条断言钉在一起,谁也漂不走。
	CodeRuntimeAborted         = -32013
	CodeRuntimeSessionNotFound = -32014
	// CodeRuntimePeerExecutionUnavailable:会话钉住的执行目标(agentred)当前
	// 不可用。它与上面五个不同,不对应任何 agentruntime sentinel —— 那条链路是
	// daemon 回给 remote 客户端,而这一条是桌面端 peer 回给浏览器。浏览器靠它把
	// 「执行目标不可用」与普通拒绝分开。
	CodeRuntimePeerExecutionUnavailable = -32015

	// ── remotefs.* ─────────────────────────────────────────────────────────
	//
	// 「浏览远端机器任意绝对路径」那一族。

	CodeRemoteFSPathRefused = -32030
	CodeRemoteFSPermDenied  = -32031
	CodeRemoteFSNotFound    = -32032
	CodeRemoteFSNotDir      = -32033
	CodeRemoteFSMkdirExists = -32034
	CodeRemoteFSInvalidName = -32035

	// ── workspacefs.* ──────────────────────────────────────────────────────
	//
	// 「浏览某个会话已解析出的工作目录」那一族,与 remotefs.* 刻意分开。

	CodeWorkspaceFSPathRefused      = -32040
	CodeWorkspaceFSBaselineRequired = -32041
	// CodeWorkspaceFSNoCwd:调用方没给工作目录。与「越界」分开 —— cwd 为空是
	// 会话配置问题,不是路径问题。
	CodeWorkspaceFSNoCwd = -32042
	// CodeWorkspaceFSNotFound:relPath 所指的文件在那台机器上不存在。越界判定
	// 在前,所以 root 之外的路径无论存不存在都只会得到 PathRefused ——
	// 这个码不能成为「那台机器上有没有这个文件」的探测器。
	CodeWorkspaceFSNotFound = -32043

	// ── project.* ──────────────────────────────────────────────────────────

	// CodeProjectNotSynced:这台机器上没有这个同步标识的项目。它与「写失败了」
	// 必须分得开 —— 项目可以先在 web 上建出来,那一刻目标机器可能还没拉到这一行,
	// 等一会儿就好;折进通用失败会让用户去查权限和磁盘。
	CodeProjectNotSynced    = -32050
	CodeProjectInvalidPath  = -32051
	CodeProjectPathNotFound = -32052

	// ── transcriptImport.* ─────────────────────────────────────────────────

	// 这三个码此前住在桌面仓 internal/pkg/transcriptimport/wire 里,自己声明的是
	// -32050..-32052 —— 与上面 project.* 那一段逐个撞上,而那个包在本包的守卫视野
	// 之外,所以没有任何地方会红。搬进来的第一次运行,守卫就点名了这次撞号,这一段
	// 因此改到 -32060 起。
	//
	// 改的是**过线的值**。它安全,是因为今天没有任何消费方在解这三个码:产出侧经
	// wireinbound 的 transcriptImportError → ToRPCError 折上线,而反向的 FromRPCError
	// 一个调用点都没有(agentre-server 也不解)。混版本期最坏的后果是「认不出来,
	// 落回泛化错误」,与今天的行为一致。
	CodeTranscriptImportBackendUnavailable = -32060
	CodeTranscriptImportTranscriptOpen     = -32061
	CodeTranscriptImportSessionInUse       = -32062
)
