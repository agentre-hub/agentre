/**
 * 每种宿主答得出的 RPC 方法(rpcMethods 的键名)。
 *
 * 本文件由 Go 生成器产出,**不要手改** —— 手改会被下一次重新生成覆盖,
 * 而且 TestGeneratedTSFresh 会立刻变红。
 *
 * 真理源:  internal/pkg/wireinbound 的 Contract() 与 KnownGaps()
 *          (键名取自 frontend/packages/agentre-wire/src/rpc-methods.ts)
 * 生成器:  internal/pkg/agentruntime/runtimes/remote/wire/tsgen_test.go
 * 重新生成:
 *
 *   WIRE_TS_WRITE=1 go test ./internal/pkg/agentruntime/runtimes/remote/wire/ -run TestWriteTSCodec
 *
 * 边界:这份产物讲的不是 wire 上有什么方法,而是**哪一种执行端答得出它**。
 * 同一个方法枚举有四类调用方、两种执行端,而调用方并不总能选被调方是哪一种:
 * 浏览器按设备指纹拨号,设备表里 desktop 与 agentred 混在一起。于是「这一侧
 * 没实现这个方法」不是内部细节 —— 它是用户按下按钮之后什么都不发生。
 *
 * 元素是 rpcMethods 的**键名**(engineScan),不是 Go 枚举名也不是 wire 上的
 * 方法字符串:消费方要拿它和自己的 rpcMethods.X 调用点对上。
 *
 * 不全:Contract() 里有几个方法在 rpc-methods.ts 里没有 descriptor(至今只有
 * Go 后端经 wirecall 在发,浏览器没有调用点),它们因此不出现在这两个数组里。
 * 那不是「答不出」,而是「TS 侧没有这条调用」—— 拿这两个数组去对**非浏览器**的
 * 调用点会漏掉它们。哪天有人给它们补上 descriptor,生成器那边会立刻判红。
 *
 * KnownGaps() 记下的「该答而答不出」已经减掉:数组名叫「答得出的方法」,
 * 留着一条已知缺口就是替它撒谎。
 *
 * 格式:生成器直接输出 Prettier(printWidth 80,本仓默认配置)的形态,
 * 与手写代码同一套 ESLint 规则,没有整文件豁免。格式化是产物的一部分 ——
 * 若放到生成之后当外部工序,「重新生成 → 逐字节比对」的守卫会永久误报。
 */

/** 桌面端宿主答得出的方法(rpcMethods 的键名)。由 agentre 的 wireinbound.Contract() 生成。 */
export const desktopAnsweredMethods: readonly string[] = [
  "authAccount",
  "sessionList",
  "sessionCounts",
  "sessionAttach",
  "sessionPull",
  "sessionPendingWaiters",
  "sessionDelete",
  "setModelTarget",
  "setSessionReasoningEffort",
  "runtimeCapabilities",
  "runtimeRun",
  "runtimeSteer",
  "runtimeCancelSteer",
  "runtimeAbort",
  "runtimeSetPermissionMode",
  "runtimeSubmitAnswer",
  "runtimeSubmitToolPermission",
  "skillCatalog",
  "skillCommands",
  "remoteFsListDir",
  "remoteFsMkdir",
  "workspaceFsReadFile",
  "workspaceFsGitFileContent",
  "engineScan",
  "engineTest",
  "cliResolvePath",
  "projectSetLocalPath",
  "projectClearLocalPath",
];

/** agentred 宿主答得出的方法(rpcMethods 的键名)。 */
export const agentredAnsweredMethods: readonly string[] = [
  "authAccount",
  "sessionList",
  "sessionCounts",
  "sessionAttach",
  "sessionPull",
  "sessionPendingWaiters",
  "sessionDelete",
  "setModelTarget",
  "setSessionReasoningEffort",
  "runtimeCapabilities",
  "runtimeRun",
  "runtimeSteer",
  "runtimeCancelSteer",
  "runtimeAbort",
  "runtimeSetPermissionMode",
  "runtimeSubmitAnswer",
  "runtimeSubmitToolPermission",
  "skillCatalog",
  "skillCommands",
  "remoteFsListDir",
  "remoteFsMkdir",
  "workspaceFsReadFile",
  "workspaceFsGitFileContent",
  "engineScan",
  "engineTest",
  "cliResolvePath",
  "engineDiscover",
  "agentredSelfUpdate",
  "authPair",
  "authConnect",
  "healthPing",
  "claudeCodeUsage",
  "llmUpsert",
  "skillsList",
  "cliProbe",
  "runtimeDrainPending",
  "runtimeStopBackgroundTask",
  "runtimeGoalGet",
  "runtimeGoalSet",
  "runtimeGoalClear",
  "terminalOpen",
  "terminalWrite",
  "terminalResize",
  "terminalClose",
  "workspaceFsListDir",
  "workspaceFsSearchFiles",
  "workspaceFsGitBranches",
  "workspaceFsGitState",
  "workspaceFsGitChanges",
];
