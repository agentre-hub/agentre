package rpcerror

// 领域错误码 —— 本包是它们的唯一主人。
//
// 这些数字是**过线的稳定协议值**:对端按码分支,不按 message 文本。它们住在共享
// module 里,消费方(agentre-server)直接引用,不手抄魔数。
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
//	-32070..-32075   portforward.*
//	-32600..-32700   JSON-RPC 标准码(见 error.go)
//	-32800           取消(见 error.go)
//
// 加新码之前先给它的方法族划一段:segments_test.go 的撞号守卫会 AST 扫本包的
// 全部 Code* 常量,既不许两个名字共用一个数字,也不许有码落在所有段位之外。
// 那张段表与这段注释是同一件事的两种写法,守卫只认前者。
//
// 守卫只看得见**住在本包的**码:在别处自己声明的码可能与这里的段位逐个撞上,而编译器
// 与守卫都看不见。
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

	CodeTranscriptImportBackendUnavailable = -32060
	CodeTranscriptImportTranscriptOpen     = -32061
	CodeTranscriptImportSessionInUse       = -32062
)

// ── portforward.* ──────────────────────────────────────────────────────
//
// 「访问设备 127.0.0.1 上某个已声明端口」那一族。段位取 -32075..-32070,前面两个
// 十位段都避开:-3205x 今天被 project.* 与桌面仓 internal/pkg/transcriptimport/wire
// 各占一次(见本文件开头的盲区那段);-3206x 则是 transcriptimport.* 搬进本包后
// 拿到的段位。往这两段里塞新码等于给一次已知撞号再加一层。
//
// 这几个码的分辨率是产品要求而不是洁癖:「等那台机器回来」「去把服务起起来」
// 「那个端口压根没被声明出来」是用户要做的三件不同的事,折进一个笼统失败就等于
// 让用户去猜。
const (
	// CodePortForwardNotDeclared:这台设备上没有这个端口的声明(从没建过,或已被
	// 删除)。端口白名单只由设备判定,调用方不持有授权。
	CodePortForwardNotDeclared = -32070
	// CodePortForwardDisabled:声明还在,但被停用了。与上一个分开 —— 停用是一次
	// 可撤销的开关,界面据此提示「把它打开」而不是「重新建一条」。
	CodePortForwardDisabled = -32071
	// CodePortForwardNoListener:端口通过了声明集判定,但那台设备的环回地址上
	// 没有服务在监听(端口写错了,或服务还没起)。
	CodePortForwardNoListener = -32072
	// CodePortForwardStreamNotFound:write / close / ack 指向的流不存在 —— 它已经
	// 收尾,或从没 open 过。与「设备拒绝了这次 open」必须分得开:前者是调用方的
	// 状态机落后了一步,后者是这次访问根本不被允许。
	CodePortForwardStreamNotFound = -32073
	// CodePortForwardPortTaken:新增声明时这个端口在这台设备上已经声明过。端口
	// 在一台设备下唯一,所以它是一次可以就地改正的输入错误,不是写失败。
	CodePortForwardPortTaken = -32074
	// CodePortForwardInvalidPort:端口号不在 1..65535 内。
	CodePortForwardInvalidPort = -32075
)
