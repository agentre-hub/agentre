# 远端的「文件不存在」：给 workspacefs 协议补上那一个错误码

> Status: Approved
> Owner: 桌面端 / agentred 协议
> Last updated: 2026-09-08

**Objective:** 让**远端**会话里读一个已被删掉的文件时，预览面说「文件不存在」且不给动作按钮——与本机会话从 2026-09-07 起就有的行为一致。

**Hard invariant:**

- **方法集不变，协议版本号不变，`pkg/wire` 生成物不变。** 本轮新增的是一个稳定 wire **错误码**，不是消息字段；`internal/pkg/wireversion` 的两个常量与 `methodset_test.go` 的两条守卫都不动。
- **两个方向都优雅降级。** 旧 daemon 对新 host、新 daemon 对旧 host，行为都退回今天的「未归类失败」，不得出现静默错渲染。
- **判不出来的失败仍作为错误抛出，不制造第四种状态**——沿用 `2026-09-07-preview-failure-classification.md` 的口径。
- **本机分支一行不改**，`agentre-server` 的 Go 一行不改。
- **越界优先于不存在**：cwd 之外的路径无论存不存在都仍是 `pathRefused`，新码不得成为探测 cwd 外文件是否存在的通道。

## Problem

1. **远端会话上「文件不存在」这一态不可达，而且代码自己写着这件事。** `internal/service/workspace_fs_svc/svc.go:344-348` 的远端分支注释：「远端的「文件不存在」本轮判不出来 —— wire 没有对应错误码，mapCallErr 只认 PathRefused / BaselineRequired，它因此仍落到未归类的兜底上」。`internal/pkg/workspacefs/wire/wire.go:42-44` 确认只有三个码：`pathRefused` / `baselineRequired` / `noCwd`。

2. **后果正是上一轮刚在本机分支消灭的那个东西。** 删掉的文件在远端会话里显示「读取工作目录失败」（`internal/pkg/code/zh_cn.go` 对 `WorkspaceFsReadFailed` 的本地化）外加一个点下去必然还是失败的「重试」按钮。`2026-09-07-preview-failure-classification.md` 的运行期报告把这一形态在本机分支上判为 `does not hold` 并修好了，远端分支原样留着。

3. **同一个缺口挡住了控制台。** `2026-09-06-console-file-preview.md` 的「失败与恢复」要求「文件已不存在：如实显示「文件不存在」」，而浏览器控制台的每一条会话都是远端会话——它经中继调的就是这个 `workspacefs.readFile`。缺这个码，控制台那一半无论怎么接线都到不了这一态。

4. **`remotefs.*` 早就有这个码，`workspacefs.*` 没有。** `internal/pkg/remotefs/wire/wire.go:30` 的 `ErrCodeNotFound = -32032`，浏览器侧 `agentre-server/frontend/src/lib/remotefs.ts:127` 直接按它归类。两个方法族刻意分开（`workspacefs/wire/wire.go:11-19`），于是这一族没跟着长出来。

## Actors and user stories

1. 作为在桌面端看**远端**会话转录的用户，我希望点开一个已被删掉的文件时它直说「文件不存在」，这样我知道是文件没了、不是那台机器坏了。
2. 作为下一轮要在浏览器里接预览的实现者，我希望「不存在」在协议上就是可分辨的一类，这样宿主不必去猜错误文本。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 走 **typed RPC 错误码**，不走响应字段 | daemon ↔ host 这条边界**本来就有**结构化错误通道（`wire.ToRPCError` / `FromRPCError`），与 Wails 边界那条「只过 `Error()` 字符串」的约束完全不同，上一轮之所以选视图字段正是因为那条约束在这里不存在。Rejected: 给 `WorkspaceFsReadFileResponse` 加 `unavailable` 字段 —— 新 daemon 对旧 host 会变成「成功应答 + 空正文」，旧 host 把一个已删掉的文件画成一个**空文件**，是静默错渲染，比今天的笼统错误更糟；且要改 proto、重新生成 `pkg/wire`，把一个纯增量的语义变成一次生成物改动 |
| 2 | 码值 **-32043**，紧接现有 workspacefs 段 | `-32040..-32042` 是本族已用段，`-32030..-32035` 属 remotefs，`-32010..-32014` 属 agentruntime remote（`workspacefs/wire/wire.go:15-18` 已写明这条分段约定）。`-32043` 空闲 |
| 3 | 归类成**视图态**这一步落在 `ReadFile` 自己的远端分支；wire 码 → host 错误码的**翻译**落在共用的 `mapCallErr` | 两步分开是被既有结构逼出来的：`callWorkspace` 在返回前就已经把 RPC 错误过了一遍 `mapCallErr`（`svc.go` 的 `callWorkspace`），远端分支拿到的 `cerr` 里已经没有原始码了——这与「对端离线」今天的走法完全同构（`mapBorrowErr` 产出错误码，`ReadFile` 的分支把它变成视图态）。`mapCallErr` 被四个方法共用，但**只有 `readFile` 这一个 handler 会产出新码**（决策 5），其余方法的失败面因此一个字都不会变。Rejected: 给 `ReadFile` 单开一条绕过 `mapCallErr` 的调用路径 —— 要把借租约那段复制一份，为一个码换来一份会漂移的副本 |
| 4 | **协议版本号不动** | 方法集未变（`methodset_test.go` 的指纹不变），且新增可选错误码两个方向都优雅降级（见下「兼容与降级」）。Rejected: 抬 `Protocol` 与 `MinSupported` 到 0.4.0 —— 窗口是一个点，抬了就把所有还没升级的远端机器挡在握手外，对一个纯增量的可选码是重锤；Rejected: 张开窗口成 0.3.0..0.3.1 —— 要改 `methodset_test.go:70` 那条「窗口守恒律」守卫，是一次**协议政策**变更，比本轮目标大得多 |
| 5 | 本轮只补 `readFile` 一个方法 | 「不存在」在预览这条链路上是用户看得见的一类；`listDir` 的目录不存在与 `gitFileContent` 的空基线各有自己的既有语义，跟着改会把一轮变成四轮。Rejected: 一次把四个方法都补上 |
| 6 | 越界判定**优先于**不存在判定 | 叶子包今天就是这个顺序（`internal/pkg/workspacefs/readfile.go` 先 `resolveRelPath` 再 `EvalSymlinks`），保持它意味着 cwd 之外的路径永远只回 `pathRefused`，新码不会变成「那台机器上有没有这个文件」的探测器 |

## 远端会话读一个不存在的文件

**前提**：用户在桌面端看着一条**远端**会话，转录或目录侧栏里有一条指向会话工作区内文件的路径，而那个文件此刻在那台机器上已经不存在了。**动作**：点开它。**结果**：预览面显示「文件不存在」，**不带任何动作按钮**——与本机会话上今天的表现逐字一致。

这一态是终态：转录里的路径来自当时那次工具调用，之后被删掉是正常情况，重试不会让它回来。

**owned failure**：那台机器**离线**时仍然是「够不着」+ 重试（离线判定在借用租约那一步就发生，早于这次调用），不受本轮影响。判不出来的失败仍按未归类处理：如实显示它自己的文案 + 重试。

## 协议这一侧

`workspacefs.readFile` 新增一个稳定 wire 错误码，语义是：**目标 relPath 在那台机器上不存在**（含路径中间某一段不存在、以及符号链接断链）。它与既有三个码互斥：

- 相对路径越出工作根 → 仍是 `pathRefused`（决策 6）。
- 工作根为空 → 仍是 `noCwd`。
- 读到了但不该渲染正文（过大 / 二进制）→ 仍是成功应答上的 `tooLarge` / `binary` 视图字段，本轮不动。

host 侧收到这个码时，先把它翻成 host 自己错误码体系里的一个新成员（与 `WorkspaceFsDeviceOffline` 同段、同性质：一个只在服务层内部流转、用来携带分类的码），再由 `ReadFile` 的远端分支把它变成 `ReadFileView` 上**已经存在**的「不存在」原因取值并**正常返回**（不报错）——那个取值、前端对它的呈现、以及「终态不给动作按钮」的规则都是上一轮定的，本轮一个字不改，只是让远端分支第一次能产出它。

## 兼容与降级

三种组合都必须是可预期的，且都不需要抬协议版本：

| host | daemon | 行为 |
| --- | --- | --- |
| 新 | 新 | 删掉的文件 → 「文件不存在」，无动作按钮 |
| 新 | 旧（没有这个码） | 旧 daemon 回一个笼统失败 → host 认不出 → **未归类**：笼统文案 + 重试，即今天的行为 |
| 旧 | 新 | 旧 host 的 `FromRPCError` 认不出 `-32043` → 原样返回 → 未归类：同样是今天的行为 |

「未归类」这条回落路径不是本轮新造的，它是共享包早就定下的契约（`file-preview/ports.ts:42`）。

## 桌面端可见的变化

只有一处：**远端**会话里点开一个已被删掉的文件，从「读取工作目录失败 + 重试」变成「文件不存在」且无按钮。本机会话、其余六个态、侧栏与转录的入口判定全部不变。

## Out of scope

- **控制台那一半的接线**（挪 pin、接 `previewFile`、预览分栏、Monaco 懒加载）——下一轮，`2026-09-06-console-file-preview.md` 的跨仓第 2 步。
- `listDir` / `gitFileContent` / `searchFiles` 的「不存在」分类。
- 端口转发（`2026-09-06-device-port-forward.md`，再下一轮）。
- 协议版本窗口政策的任何改动。

## Testing decisions

| Seam | What it verifies | Prior art |
| --- | --- | --- |
| daemon 的 `ReadFile` handler 单测 | 工作根内不存在的 relPath → 新错误码；**越界的路径无论存不存在都仍是 `pathRefused`**（决策 6 的安全断言）；空根仍是 `noCwd` | `internal/daemon/workspacefs/handler_readfile_test.go` 既有四例 |
| workspacefs wire 包的双向翻译单测 | 新 sentinel ⇄ 新码往返；未知码仍原样返回 | `internal/pkg/workspacefs/wire/wire_test.go` |
| `workspace_fs_svc` 的服务层单测 | 远端调用回新码时应答带「不存在」原因**且不返回错误**；回未知码时仍作为错误抛出；本机分支行为不变 | `internal/service/workspace_fs_svc/svc_test.go` 上一轮新增的三例 |

运行期观察：真实远端设备上删一个文件再点开它。这需要 `coding.local` 上的 agentred 换成带本轮改动的二进制——与上一轮同样的前提，收尾时按当时的授权情况决定，未获授权则如实记为 `not observed`。

**不能自动化的一条**：「旧 daemon + 新 host」这一格需要两个版本的二进制同时在场，本轮不搭这个装置；它由收尾的源码复核覆盖（认不出的码走既有兜底，没有新分支），并在运行期报告里如实标注未观察。

## Open questions

<!-- 空 -->
