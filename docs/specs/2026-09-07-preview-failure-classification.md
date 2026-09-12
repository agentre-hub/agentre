# 预览失败态：让「文件不存在」与「对端离线」在生产路径上真的到达

> Status: Draft
> Owner: 桌面端 / 控制台前端 + 会话文件服务
> Last updated: 2026-09-07

**Objective:** 让预览面的失败分类在**真实的宿主路径**上到达——文件被删掉时说「文件不存在」且不给动作，对端离线时说「够不着」且给重试——而不是两种都退化成同一句笼统错误加一个必然失败的重试按钮。

**Hard invariant:**

- 共享包里仍然只有一份预览渲染实现，宿主不得长出第二份判定。
- 转录里内联图片（`readWorkspaceFile`）的失败行为**一字不变**：今天读不到就是 reject，改完还是 reject。
- 不新增 Wails 方法，不改 `pkg/wire` 与生成物，服务端 Go 一行不改（本规格只动桌面端进程内的 `workspace_fs_svc` 与前端）。
- `2026-09-06-console-file-preview.md` 的既有判定不被推翻：可预览性仍由 `previewKind` 单点给出，转录去向仍跟随 `files.open_action`。

## Problem

1. **两个分类失败态在生产上都无法到达。** 共享包把归类责任交给宿主：`frontend/packages/agentre-ui/src/file-preview/ports.ts:41` 写明宿主应 reject 一个带 `kind` 的错误，「不带 `kind` 的失败按未归类处理：如实显示它自己的文案 + 重试」。而**全工作区没有任何宿主贴过这个标记**——`kind` 的赋值只出现在包自己的两条单测里（`file-preview-panel.test.tsx:223`、`:235`），桌面端装配根把 Wails 调用原样透传（`frontend/src/components/agentre/file-preview/file-preview-panel.tsx:108`）。因此 `notFound` 与 `offline` 双双不可达。

2. **「文件不存在」在运行期被实测为不成立。** 2026-09-07 的运行期验证（证据：`e2e/scratch/console-file-preview/report.md` 的 V6，截图 `screenshots/v6-not-found.png`）：在会话工作区建 `ghost.md`、从目录侧栏点开确认可预览、`rm` 掉之后再点那一行，预览面显示「读取工作目录失败」并渲染了「重试」按钮。这与 `2026-09-06-console-file-preview.md`「失败与恢复」的两句直接冲突：「文件已不存在：如实显示「文件不存在」」，以及「只有「对端离线」给重试按钮。其余三种是终态」。

3. **根因在 Go 侧没有「文件不存在」这个概念。** `internal/service/workspace_fs_svc/svc.go:578-586` 的 `mapLocalErr` 只认 `ErrPathRefused` 与 `ErrBaselineRequired`，其余一律落进兜底 `code.WorkspaceFsReadFailed`；`internal/pkg/code/zh_cn.go:253` 把它本地化成「读取工作目录失败」——正是实测看到的那句。`internal/pkg/code/code.go` 里**没有**任何 file-not-found 码。

4. **「对端离线」很可能同样坏着，只是至今没被观察到。** 它与 `notFound` 共用同一条不存在的通道。运行期没能验证：`coding.local` 上的 agentred 与本分支协议版本不一致、用户不授权重新部署，V7 因此记为 `not observed`（同上报告）。本规格按「它也坏着」处理，并在同一条通道上一并修好。

5. **测试打在了错误的一侧。** 包的两条失败态单测各自注入一个**已经贴好 `kind`** 的 error，因此只证明了包内的分支逻辑，从未证明任何宿主能产出那个标记。收尾的两个可写轴都没抓到：spec 轴看不到宿主适配器里缺的那一步，code 轴不读 spec、看到的是一组形状良好的单测。

6. **悬停浮层承诺了一个它不知道的去向。** `frontend/packages/agentre-ui/src/transcript/rich-link.tsx:262` 对 cwd 内的可预览路径固定渲染 `richLink.openWithDefaultApp`（「点击用系统默认应用打开」），而设置为「在 agentre 内预览」时点下去开的是内置预览（证据：`screenshots/v1-transcript-preview.png`，浮层与预览栏同框）。同一句文案在控制台宿主上会更离谱——那侧根本没有「外部应用」这条退路。

## Actors and user stories

1. 作为在预览里点开一个已被删掉的文件的用户，我希望它直说「文件不存在」而不是给我一个笼统错误，这样我知道是文件没了、不是工具坏了。
2. 作为看到终态的用户，我希望不要给我一个点下去必然还失败的按钮，这样我不会白点。
3. 作为对端临时离线的用户，我希望预览面说清是那台机器够不着，并给我一个真的有意义的重试。
4. 作为悬停在文件链接上的用户，我希望提示不要承诺一个不会发生的去向。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 失败分类作为**视图态**随成功应答返回，与 `binary` / `tooLarge` 同形 | 用户决定。`ReadFileView` 已经用布尔标志表达「读到了，但不该渲染正文」（`svc.go:159-164`）；「文件不存在 / 对端离线」是同一类事实。且与 `chat_svc` 的既有先例同思路——`CwdUnavailableReason`（`internal/service/chat_svc/types.go:625-632`）正是为了同一个约束而生，其注释写明「Wails 边界只过 Error() 字符串，没有结构化通道」。Rejected: 新增 not-found 错误码并给 Wails 错误造一条带码通道 —— 结构化错误通道今天不存在，要新造，是三个选项里最大的一个；Rejected: 宿主按本地化字符串匹配归类 —— 文案已本地化，换语言即碎，且包内注释已明确「不去猜字符串」，宿主去猜同样是猜 |
| 2 | 取值用**结构化字符串枚举**而不是两个布尔 | 三态互斥（正常 / 不存在 / 离线），布尔组合会造出无意义的第四种状态。与 `CwdUnavailableReason` 的取值风格一致 |
| 3 | `readWorkspaceFile`（转录内联图片）把新字段**转回 reject** | 该端口的消费者只需要「成不成」，今天读不到就是 reject。让适配器在边界上还原旧契约，改动就止步于预览这一条路径，内联图片零行为变化 |
| 4 | 悬停浮层改成**不承诺去向**的中性一句，两档共用 | 用户决定。浮层在**点击之前**渲染，而 `2026-09-06-console-file-preview.md` 让去向只在点击时由 `previewFile` 的布尔握手定下来；要在渲染期知道去向就得给包新增一个去向查询端口，与点击期的握手并存成两个真相源。中性文案顺带把控制台那侧也修对了。Rejected: 两档各一句 + 新增渲染期去向查询端口 —— 双真相源可能互相矛盾；Rejected: 整行不显示 —— 丢掉一个真实的 affordance |
| 5 | cwd **之外**那条浮层的文案保持不动 | `rich-link.tsx:304` 那一处对应的是越出 cwd 的路径，它在任何设置下都只能交给外部应用，今天这句话是对的 |

## 失败分类怎么到达

**前提**：用户在预览面里打开会话工作区内的一个文件。**动作**：预览取数。**结果**：读得到就渲染正文；读不到时，预览面按下面三类之一如实说明，而不是一律笼统报错。

会话文件读取的应答多出一个**结构化的不可用原因**字段，取值互斥：

- 空 —— 正常，正文有效（含 `binary` / `tooLarge` 这两个既有视图标志）。
- **文件不存在** —— 目标路径在那台机器上已经没有了。
- **对端离线** —— 那台机器现在够不着（含中继断开、借用连接失败）。

原因由**服务层**判定：它是唯一同时看得见「本机分支的叶子包 sentinel」与「远端分支的借用/调用失败」的地方。判不出来的失败仍然按今天的方式作为错误抛出，前端照旧回落到未归类处理——本规格不制造第四种状态。

桌面端装配根把这个字段翻译成共享包已经定义好的失败标记，交给包里那份面板。**归类的产出点因此第一次落在生产路径上**，而不只是测试夹具里。

内联图片那条端口不受影响：它在边界上把「不可用」重新变回一次失败，行为与今天完全一致。

## 三个失败态各自长什么样

沿用 `2026-09-06-console-file-preview.md`「失败与恢复」已经定下的口径，本规格只负责让它们真的到达：

- **文件不存在**：显示「文件不存在」，**不带任何动作按钮**。终态——转录里的路径来自当时那次工具调用，之后被删掉是正常情况。
- **对端离线**：显示「这台机器现在够不着」，**并且只有这一态给重试**。重试重新取数。
- **未归类的失败**：如实显示它自己带来的那句文案，并给重试。这是兜底，不是前两者的替代品。

`tooLarge`（「文件过大，无法预览」）与 `binary`（「二进制文件，无法预览」）两态今天已经正确、且都不带动作按钮，本规格不动它们。

## 悬停浮层的提示

**前提**：用户悬停在转录里一条指向会话工作区内、且可预览的文件路径上。**动作**：浮层展开。**结果**：页脚那一行只陈述「点一下会打开这个文件」，不指名会由哪一侧打开。

越出 cwd 的那条浮层保持今天的措辞不变——那条路径在任何设置、任何宿主下都只能交给外部应用。

两种语言都要给出对应文案；这条文案归共享包所有，两端共用一句。

## Out of scope

- 给 Wails 边界新造结构化错误通道（决策 1 已否）。
- 让浮层在渲染期知道点击去向（决策 4 已否）。
- `tooLarge` / `binary` 两态的任何改动。
- 服务端（`agentre-server`）与 `agentred` 的任何改动；`pkg/wire` 与生成物零改动。
- 控制台宿主接上这条分类——那属于 `agentre-server` 侧尚未开始的那一轮，本轮只保证共享包的契约与桌面端的产出点是对的。

## Testing decisions

| Seam | What it verifies | Prior art |
| --- | --- | --- |
| `workspace_fs_svc` 的服务层单测 | 目标文件不存在时应答带「文件不存在」原因且**不返回错误**；远端借用失败时带「对端离线」原因；判不出来的失败仍作为错误抛出 | `internal/service/workspace_fs_svc/svc_test.go` 既有用例 |
| **桌面端装配根**的组件测试 | 打在生产路径上：让 Wails 绑定返回带原因的应答，断言面板真的落到对应失败态——「不存在」无动作按钮、「离线」有重试。**不得**通过注入一个预先贴好标记的 error 来满足 | `frontend/src/components/agentre/__tests__/file-preview-panel.test.tsx` |
| 桌面端转录端口测试 | `readWorkspaceFile` 遇到带原因的应答时仍然 reject，内联图片的失败行为不变 | **无**——`transcript-ports-desktop.ts` 的这个适配器今天没有任何测试覆盖，本轮要新建 |
| 共享包面板测试 | 三个失败态的渲染与动作按钮归属（既有用例保留） | `frontend/packages/agentre-ui/src/file-preview/file-preview-panel.test.tsx` |
| 共享包 `rich-link` 测试 | cwd 内浮层用中性文案且不随设置变化；cwd 外浮层文案不变 | `frontend/packages/agentre-ui/src/transcript/rich-link.test.tsx` |
| i18n 覆盖守卫 | 新增文案两种语言齐备 | `frontend/src/__tests__/i18n.test.ts`、`packages/agentre-ui/src/i18n/i18n.test.tsx` |

**已知的自动化盲区，写在这里以免再被当成已验证**：「对端离线」那一态至今**没有任何运行期观察**。上一轮尝试时 `coding.local` 上的 agentred 与桌面端协议版本不一致、用户不授权重新部署（`e2e/scratch/console-file-preview/report.md` 的 V7）。本轮的服务层与装配根测试能证明「离线原因一路走到重试按钮」，但**真实远端设备掉线**这一步仍未在真机上走过。收尾时若仍无法配对，这一条应继续如实记为 `not observed`，不得因为单测转绿就升级成 `holds`。

## Open questions

无。
