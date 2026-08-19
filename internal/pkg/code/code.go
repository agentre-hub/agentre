// Package code 集中维护 Agentre 的业务错误码与 i18n 提示。
//
// 规则：
//   - 错误码从 10000 起，向上递增，按业务域分段（10000 通用、11000 Agent、…），
//     新增时把新常量加在所属段末尾，避免插入中间导致后续值变化。
//   - 每个错误码对应一条 i18n 文案（见 zh_cn.go），运行时通过
//     i18n.NewError(ctx, code.Xxx) 返回，由 cago 自动按 ctx 语言渲染。
package code

// 业务错误码
const (
	// 通用 10000~10999
	OperationFailed  = iota + 10000 // 操作失败
	InvalidParameter                // 参数错误
	NotFound                        // 资源不存在
	ServerError                     // 服务器内部错误
)

// LLM 供应商 11000~11999
const (
	LLMProviderNotFound             = iota + 11000 // LLM 供应商不存在
	LLMProviderNameDuplicated                      // LLM 供应商名称已存在
	LLMProviderInvalidType                         // LLM 供应商类型不支持
	LLMProviderFetchModels                         // 拉取模型列表失败
	LLMProviderDisabled                            // LLM 供应商已停用
	LLMProviderNoEnabledDefault                    // 启用前必须设置属于该供应商的启用默认模型
	LLMProviderReferenced                          // 供应商被 Backend/Session/Route 引用，删除需二次确认
	LLMProviderModelNotFound                       // 模型不存在
	LLMProviderModelNotOwned                       // 模型不属于该供应商
	LLMProviderModelDisabled                       // 模型已停用
	LLMProviderModelIsDefault                      // 默认模型不能停用或删除，请先指定其它默认模型
	LLMProviderModelReferenced                     // 模型被 Backend/Session/Route 引用，删除需二次确认
	LLMProviderModelConfirmRequired                // 修改被引用模型的 model_id 需要二次确认
	LLMProviderDefaultModelInvalid                 // 供应商未配置可用的默认模型
	LLMProviderModelTargetInvalid                  // 固定模型目标已失效（Provider/模型缺失/停用/不兼容）
)

// Agent 后端 12000~12999
const (
	AgentBackendNotFound            = iota + 12000 // Agent 后端不存在
	AgentBackendNameDuplicated                     // Agent 后端名称已存在
	AgentBackendInvalidType                        // Agent 后端类型不合法
	AgentBackendTypeUnsupported                    // Agent 后端类型暂未支持
	AgentBackendLLMProviderRequired                // builtin 后端必须绑定 LLM 供应商
	AgentBackendCLIPathNotAllowed                  // builtin 后端不允许填写 CLI 路径
	AgentBackendLLMProviderNotFound                // 绑定的 LLM 供应商不存在
	AgentBackendLLMProviderInactive                // 绑定的 LLM 供应商已停用
)

// Agent 后端 12000 段补位（已用 12000~12007）
const (
	AgentBackendInUse = iota + 12008 // 删 backend 时被 Agent 引用
)

// Agent 后端 12009~（claudecode/codex 与远端 device 接入）
const (
	AgentBackendProviderTypeMismatch   = iota + 12009 // claudecode/codex 与 provider.type 不匹配
	AgentBackendUnknownAlias                          // model_routes 含未知 alias
	AgentBackendAliasProviderInvalid                  // alias 引用的 provider 不存在 / 已停用 / 类型不匹配
	AgentBackendInvalidSandbox                        // codex sandbox 不在枚举
	AgentBackendInvalidApproval                       // codex approval 不在枚举
	AgentBackendInvalidEnvJSON                        // env_json 反序列化失败
	AgentBackendReservedEnvKey                        // env_json 含 ANTHROPIC_BASE_URL 等保留键
	AgentBackendGatewayUnavailable                    // 测试时本地代理未启动
	AgentBackendInvalidReasoningEffort                // reasoning_effort 不在 6 档枚举（""/low/medium/high/xhigh/max）
	AgentBackendInvalidDevice                         // device_id 引用的远端设备不存在或已下线
)

// Agent 后端 12019~（piagent 绑定自定义供应商）
const (
	AgentBackendProviderModelRequired = iota + 12019 // 绑定供应商的 backend 要求 provider.Model 非空（--model 必须能选中模型）
)

// App 设置 15000~15999
const (
	AppSettingNotFound      = iota + 15000 // 设置项不存在
	AppSettingInvalidPort                  // 端口越界或非数字
	AppSettingInvalidHost                  // 监听地址非合法 IP
	AppGatewayRestartFailed                // 应用并重启时绑定端口失败
	AppSettingInvalidBool                  // 布尔设置项取值非法
)

// Hook / 信号源 16000~16999
const (
	HookSourceNotFound          = iota + 16000 // 信号源不存在
	HookSourceNameDuplicated                   // 信号源名称已存在
	HookInvalidSourceType                      // 信号源类型不支持
	HookInvalidConfig                          // 信号源配置 / payload JSON 不合法
	HookRuleNotFound                           // 路由规则不存在
	HookRuleTargetAgentNotFound                // 路由目标 Agent 不存在
	HookRuleFallbackImmutable                  // 兜底规则不可删除
	HookEventNotFound                          // Hook 事件不存在
	HookInvalidEventStatus                     // Hook 事件状态不合法
	HookNotFound                               // 脚本 Hook 不存在
	HookNameDuplicated                         // Hook 名称已存在
	HookInvalidInterpreter                     // 不支持的解释器
	HookInvalidSchedule                        // 调度表达式无效
)

// Department 13000~13999
const (
	DepartmentNotFound            = iota + 13000 // 部门不存在
	DepartmentNameDuplicated                     // 部门名称在同父下已存在
	DepartmentInvalidColor                       // 部门主题色不合法
	DepartmentParentNotFound                     // 父部门不存在
	DepartmentParentInactive                     // 父部门已停用
	DepartmentCircularReference                  // 父部门指向自身或形成环
	DepartmentLeadNotInDepartment                // Lead 必须是本部门直挂的 Agent
	DepartmentHasChildren                        // 保留：strict 模式下子树非空
)

// Agent 14000~14999
const (
	AgentNotFound           = iota + 14000 // Agent 不存在
	AgentNameDuplicated                    // Agent 名称全局重复
	AgentInvalidColor                      // 头像配色不合法
	AgentInvalidPayload                    // prompt / skills JSON 反序列化失败
	AgentDepartmentRequired                // 非 CEO Agent 必须指定部门
	AgentDepartmentNotFound                // 指定的部门不存在
	AgentDepartmentInactive                // 指定的部门已停用
	AgentBackendRequired                   // 非 CEO Agent 必须绑定后端
	AgentBackendInvalidRef                 // 引用的 backend 不存在或已停用
	AgentSystemImmutable                   // CEO 不能改部门 / 删 / Move
	AgentParentNotFound                    // 指定的上级 Agent 不存在
	AgentCircularReference                 // 上级 Agent 指向自身或形成环
	AgentAvatarInvalid                     // 头像格式不合法（需 PNG / JPEG / WEBP base64 data URL）
	AgentAvatarTooLarge                    // 头像超出 2MB 上限
)

// Chat 会话 / 消息 17000~17999
const (
	ChatSessionNotFound               = iota + 17000 // 会话不存在
	ChatAgentNotChattable                            // Agent 未绑定可对话的内置后端
	ChatBlocksMalformed                              // 消息内容块解码失败
	ChatSendInFlight                                 // 该会话已有进行中的对话
	ChatProviderFailed                               // LLM 供应商调用失败
	ChatInvalidRole                                  // 消息 role 不合法
	ChatTextTooLong                                  // 单条消息文本过长
	ChatTitleTooLong                                 // 会话标题过长
	ChatBackendGatewayUnavailable                    // claudecode / codex 后端关联了 LLM 供应商但本地网关未就绪
	ChatMessageNotFound                              // 消息不存在
	ChatRegenerateNotAssistant                       // 重新生成只能作用于 assistant 消息
	ChatRegenerateNoUserAnchor                       // 目标 assistant 之前找不到 user 消息（不可恢复的脏数据）
	ChatRegenerateUnsupported                        // 该后端尚未支持中段重新生成（Step 1 仅 builtin）
	ChatEditNotUser                                  // 编辑只能作用于 user 消息
	ChatProviderSessionGone                          // CLI 的 provider 原生 Session 已不存在，本会话已重置
	ChatRemoteProviderNotConfigured                  // 远端 agentred 未配置该 provider key
	ChatAgentNoBackend                               // Agent 还没配置后端，对话前请先选择
	ChatRemoteRunInterrupted                         // 远端 agentred 重启/会话已中断，本轮被打断（不是运行失败）
	ChatRemoteDaemonUnreachable                      // 与远端 agentred 断连且重连未成功，本轮到此为止
	ChatAgentNoAvailableExecTarget                   // Agent 的有序执行目标列表非空，但逐档判定全部不可用（R15）
	ChatExecTargetOverrideUnavailable                // 手动指定的执行目标此刻不可用，拒绝钉住（R15a）
	ChatExecTargetOverrideNotInList                  // 手动指定的 agentBackendID 不在这个 Agent 的执行目标列表里
)

// Chat 排队消息（Enqueue / Steer / Cancel）17050~ 留段
const (
	ChatSteerNoActive             = iota + 17050 // 没有进行中的对话可以插入消息
	ChatSteerUnsupported                         // 当前后端不支持插入消息
	ChatSteerInternal                            // 插入消息写入失败
	ChatCancelUnsupported                        // 当前后端不支持撤回排队消息（codex turn/steer 一发即弃）
	ChatCancelNotFound                           // 排队消息已被 AI 取走或本身不存在
	ChatStopNoActive                             // 没有正在进行的对话可停止
	ChatStopInternal                             // 中止当前对话时内部错误
	ChatPermissionModeUnsupported                // 当前后端不支持运行时切换 permission mode（仅 claudecode 支持）
	ChatPermissionModeInvalid                    // 非法 mode 值（合法：default / acceptEdits / plan / bypassPermissions）
	ChatPermissionModeNoActive                   // 没有可切换的常驻会话（先发一轮消息让 CLI 起来）
	ChatPermissionModeInternal                   // 切换 mode 失败（I/O 错误 / CLI 拒绝）
	ChatCompactUnsupported                       // 当前后端不支持原生压缩
	ChatCompactNoSession                         // CLI backend 尚无 provider 原生会话可压缩
	ChatCompactInternal                          // 压缩失败
	ChatGoalUnsupported                          // 当前后端不支持目标状态
	ChatGoalNoSession                            // Codex 尚无 provider thread 可设置目标
	ChatGoalInternal                             // 目标状态操作失败
)

// Chat 启动命令 17080~
const (
	ChatLaunchCommandNotAvailable = iota + 17080 // 当前后端不支持复制启动命令
)

// Chat plan action 17090~
const (
	ChatPlanActionUnknown = iota + 17090 // PlanAction ID 无法识别(前端传错或 backend 不支持)
)

// Chat git state 17100~
const (
	ChatGitStateUnavailable = iota + 17100 // 当前 cwd 不是 git 仓库 / git 命令读取失败
)

// Chat stop background task 17110~
const (
	ChatStopBgUnsupported = iota + 17110 // 当前后端不支持停止后台任务
	ChatStopBgTaskUnknown                // 该后台任务缺少 CLI task_id（老会话）/ 已不在运行,无法停止
)

// Chat 执行目标逐档不可用说明 17120~（R15：全部不可用时逐档告诉用户为什么）
//
// 这一段是**用户可见的正文**，不是错误码：它经 PickExecTarget 渲染进
// ExecTargetNoneAvailableError.Error()、经 ListAgents / ListExecTargetAvailability
// 落进 ChattableHint / Hint 字段透到前端。因此和其它文案一样必须两份语言都有。
const (
	ChatExecTargetLineFormat             = iota + 17120 // 逐档原因的行格式：序号 / backend id / 机器 / 原因
	ChatExecTargetDeviceLocal                           // 逐档原因里指代「当前这台桌面端」的机器名
	ChatExecTargetDeviceRemote                          // 逐档原因里指代某台 agentred 的机器名（参数为 device id）
	ChatExecTargetHintBackendGone                       // 这一档引用的 backend 已不存在
	ChatExecTargetHintUnpaired                          // 本机没配对这台 agentred（R2b）
	ChatExecTargetHintOffline                           // 已配对但这台 agentred 当前离线
	ChatExecTargetHintDesktopNotRunning                 // 目标桌面端 App 没有运行（R2，与 agentred 离线区分）
	ChatExecTargetHintLocalPathMissing                  // 本机没配这个项目的路径（决策 34）
	ChatExecTargetHintRemotePathMissing                 // 那台 agentred 上没配这个项目的路径（决策 34）
	ChatBackendHintActivateProvider                     // 后端关联的 LLM 供应商缺失 / 未激活
	ChatBackendHintProviderTypeMismatch                 // 后端类型与激活的供应商类型不匹配
	ChatBackendHintRemoteProviderMissing                // 远端 agentred 未配置该供应商
	ChatBackendHintGatewayNotRunning                    // 本地网关未启动，CLI 后端暂不可用
	ChatBackendHintRemoteOpenClaw                       // 远端 OpenClaw 暂不可用
	ChatBackendHintUnknownType                          // 未知 Agent 后端类型
	ChatAgentNoBackendHint                              // 这个 Agent 还没有任何执行目标
	ChatSystemAgentNoBackendHint                        // CEO 助手还没有任何执行目标
)

// Project 18000~18999
const (
	ProjectNotFound          = iota + 18000 // 项目不存在
	ProjectNameDuplicated                   // 同级下已存在同名项目
	ProjectInvalidColor                     // 项目主题色不合法
	ProjectInvalidPath                      // 项目路径非法
	ProjectPathNotExist                     // 项目路径不存在
	ProjectParentNotFound                   // 父项目不存在
	ProjectParentInactive                   // 父项目已停用
	ProjectCircularReference                // 父项目指向自身或形成环
	ProjectAgentNotMember                   // Agent 不是该项目成员
	ProjectAgentNotFound                    // 引用的 Agent 不存在或已删除
	ProjectHasChildren                      // 项目下还有子项目
	ProjectHasActiveSessions                // 项目下还有未归档会话
	_                                       // (removed) ProjectWorktreePoolFull
	_                                       // (removed) ProjectWorktreeNotGitRepo
	_                                       // (removed) ProjectWorktreeConflict
	_                                       // (removed) ProjectWorktreeNotImplemented
	_                                       // (removed) ProjectMergeStrategyInvalid
	_                                       // (removed) ProjectAtLeastOneWorkMode
	ProjectLocalPathMissing                 // 本机未配置该项目的路径(R10,与 ProjectLocationMissing/WorkspaceFsNoCwd 均可区分)
)

// Project Location 18100~ (远端 device 路径子表)
const (
	ProjectLocationNotFound    = iota + 18100 // 项目路径不存在
	ProjectLocationInvalidPath                // 项目路径必须是绝对路径
	ProjectLocationMissing                    // 该项目尚未在所选设备上配置路径
	ProjectLocationDuplicate                  // 同 (project, device) 已有 active 路径
)

// Issue 18200~18999
const (
	IssueNotFound          = iota + 18200 // issue 不存在
	IssueTitleRequired                    // issue 标题不能为空
	IssueInvalidState                     // issue 状态非法
	IssueLabelNameRequired                // 标签名不能为空
	IssueLabelInvalidTone                 // 标签色调非法
	IssueLabelNotFound                    // 引用的标签不存在
)

// Server 接入 20300~20399
const (
	ServerURLInvalid          = iota + 20300
	ServerUnreachable         // Server 不可达
	ServerVersionMismatch     // Server 版本不兼容
	ServerLoginPending        // 等待浏览器授权
	ServerLoginExpired        // device code 已过期
	ServerLoginDenied         // 用户拒绝授权
	ServerRefreshFailed       // refresh token 失败
	ServerKeychainUnavailable // 系统 keychain 不可用
)

// 远程设备（agentred LAN）20400~20499
const (
	RemoteDeviceNotFound         = iota + 20400
	RemoteDeviceURLInvalid       // URL 非 ws:///wss:// 或缺路径
	RemoteDeviceAlreadyPaired    // 已配对该 URL
	RemoteDevicePairingInvalid   // pairing code 过期 / 无效 / 限速
	RemoteDeviceUnauthorized     // token / fingerprint 不匹配
	RemoteDeviceDialFailed       // WS dial 失败
	RemoteDeviceTOFUMismatch     // daemon_fingerprint 变了
	RemoteDeviceTLSConfigInvalid // PEM 解析失败 / 模式与字段不匹配
	RemoteDeviceKeychainFailed   // keychain set/get 失败
	RemoteDeviceTimeout          // RPC 超时（远端响应慢 / 卡死）
	RemoteCLIDetectFailed        // 远端 cli.resolvePath 返回错（PATH 扫描失败等）
	RemoteCLIProbeFailed         // 远端 cli.probe 返回错（CLI 子进程失败 / 验证失败等）
)

// Remote Runner / 跨端审批 20500~
const (
	RemoteRunnerDialFailed      = iota + 20500 // 无法连接到远端 agentred
	RemoteRunnerCallFailed                     // 远端调用失败
	RemoteRunnerConnectionLost                 // 与远端 agentred 的连接已断开
	RemoteRunnerInvalidState                   // 远端会话状态异常
	ApprovalCrossDeviceMismatch                // 跨端审批请求 ID 不匹配
)

// 远端文件系统(remotefs)20600~
const (
	RemoteFsPathRefused      = iota + 20600 // 路径被拒绝(系统目录或非法路径)
	RemoteFsPermDenied                      // 远端权限不足
	RemoteFsNotFound                        // 路径不存在
	RemoteFsNotDir                          // 目标不是目录
	RemoteFsDeviceOffline                   // 远端设备不在线 / pool borrow 失败
	RemoteFsMkdirExists                     // 同名目录已存在
	RemoteFsMkdirInvalidName                // 文件夹名非法
)

// 数据导入导出 20700~
const (
	DataBundleFormatInvalid      = iota + 20700 // 文件格式不识别
	DataBundleVersionUnsupported                // 文件版本不兼容,请升级 Agentre
	DataBundleScopeUnknown                      // bundle 含未知 scope
	DataExportEncodeFailed                      // 导出 JSON 编码失败
	DataExportWriteFailed                       // 写入导出文件失败
	DataImportReadFailed                        // 读取导入文件失败
	DataImportDanglingRef                       // 跨域引用不在导入范围内
	DataImportDuplicateLocal                    // 本地存在多条同名记录,无法自动覆盖
	DataImportRollback                          // 导入失败,所有改动已回滚
	DataImportInvalidAction                     // 未知的导入 action
)

// 会话工作目录浏览(workspacefs)20800~
const (
	WorkspaceFsNoCwd            = iota + 20800 // 会话没有可浏览的工作目录
	WorkspaceFsPathRefused                     // 相对路径越出会话工作目录
	WorkspaceFsReadFailed                      // 目录 / git 读取失败
	WorkspaceFsBaselineRequired                // 「本分支」档缺少对比基线
	WorkspaceFsDeviceOffline                   // 远端设备不在线 / pool borrow 失败
	WorkspaceFsDaemonOutdated                  // 远端 agentred 不认识 workspacefs.* 方法族
)
