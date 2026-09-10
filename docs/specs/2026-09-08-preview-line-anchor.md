# 内置预览按链接里的行号定位并选中

> Status: Approved
> Owner: 桌面端 / 控制台 前端
> Last updated: 2026-09-08

**Objective:** 转录里点一条带行号的文件链接（`src/app/script.ts:311-330`），内置预览打开这个文件后滚动到起始行并选中整段，而不是像今天这样把用户丢在文件开头。

**Hard invariant:**

- **不带行号的链接行为一个字不变。** 今天的开标签 / 复用标签 / 淘汰 / 固定语义全部照旧。
- **定位是一次性导航，不是标签状态。** 它不进持久化；重启后标签还在、不再重新定位。
- **`previewFile` 与面板的新参数一律可选。** 控制台在 pin 到新 revision 之前不传即今天的行为，两端不得因为这一轮出现「点了没反应」。
- **越界不报错。** 行号超出文件行数一律钳到末行，不产生错误态、不弹提示。
- **不碰外部打开那条路线。** `OpenPath` 仍然剥掉后缀交给系统默认应用（行号交给未来的「编辑器 URL scheme」设置项），本轮只改内置预览这一支。

## Problem

1. **行号一路走到编辑器门口就被丢掉了。** `classifyLink` 解析得出 `line`（`frontend/packages/agentre-ui/src/lib/link-classify.ts:33`，`LinkClass` 的 `line?` / `col?` 在 :8-9、:15-16），但分流处 `previewRelPath` 只回一条 relPath（`frontend/packages/agentre-ui/src/transcript/rich-link.tsx:43`），端口签名也只有两个参数（`frontend/packages/agentre-ui/src/transcript/ports.ts:114`：`previewFile?(sessionId: number, path: string): boolean`）。行号在 `rich-link.tsx:152-156` 这一步蒸发。

2. **下游三层同样没有落点。** `openPreview(sessionId, path, sourceMode)`（`frontend/src/stores/file-preview-tabs-store.ts:50-54`）与 `FilePreviewTab`（同文件 :23-29）里没有任何定位字段；`CodePreview` 拿着 `editorRef` 却不暴露任何定位入口（`frontend/packages/agentre-ui/src/file-preview/code-view.tsx:36-71`）。四层都要加一个字段，不是一处改动。

3. **能力本来就在。** `MonacoCodeEditor` 是 `monaco-editor` 的真实 `IStandaloneCodeEditor` 类型（`frontend/packages/agentre-ui/src/file-preview/monaco.ts:24`），`revealLineInCenter` / `setSelection` 直接可用，不需要新的 peer 依赖，也不需要装语言服务。

4. **用户看到的形态。** 带行号点开 = 文件从第 1 行开始显示，要自己滚到 311 行。对 `:311-330` 这种由 AI 写出来指认一段代码的引用，这等于把引用里最有信息量的那一半扔了。

## Actors and user stories

1. 作为在转录里读 AI 回复的用户，我希望点 `script.ts:311-330` 直接落到那 20 行并且它们是选中的，这样我不用自己数行、也能直接复制这一段。
2. 作为在浏览器控制台上看远端会话的用户，我希望同一条链接在控制台里也这么走，这样两端读同一段回复的体感一致。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | **滚动到起始行 + 选中整段**（`revealLineInCenter(start)` 后 `setSelection(start..end)`） | 用户决定。选区看得见范围、可直接复制，与「定位并选中到这个位置」的原话一致。为此要把 `endLine` 加回 `LinkClass`——2026-09-08 修 `:start-end` 解析那一轮只取了起始行、刻意丢了终止行，本轮把它捡回来。Rejected: 只画行高亮 decoration —— 不可复制，且要自己管理 decoration 生命周期；Rejected: `revealRangeInCenter` 居中整段 —— 20 行的范围会把视口停在 320 行附近，而不是用户点的 311 |
| 2 | **每次点击都重新定位**，定位目标带一个自增 nonce | 用户决定。用户滚走之后再点同一条链接，期望是回到那一行；而 `path + line` 不变时 React 不会重跑 effect，没有 nonce 就是「点了没反应」。Rejected: 只在标签新开时定位一次 —— 实现最简单但重复点击静默失效，用户多半当成 bug |
| 3 | **定位目标不持久化**，rehydrate 时一律为 null | 它是一次导航而不是标签的属性；持久化会让重启后第一次渲染无缘无故跳一次。store 的持久化清洗（`file-preview-tabs-store.ts:108-136`）本来就对缺字段的旧条目做整条丢弃，本字段必须显式豁免于那条规则，否则这一轮会把用户已有的标签全清空 |
| 4 | **markdown 带行号时，只有新开标签才落在 `text` 档；已开着的标签不改档位** | 用户决定。`render` 档没有行的概念（`file-preview-panel.tsx:222-227` 把 markdown 的存储档位钳到 render/text/split），新开时选 text 才定位得上；而档位是标签自身的状态、切换标签各自保留，点一条链接就把用户选定的 render 抹掉是越权。代价：标签已开在 render 时点带行号的链接只激活、不定位 |
| 5 | 端口新参数做成**可选的第三参** `anchor?: { line, endLine? }` | 跨仓库交付有一个窗口期：`agentre` 推上去到 `agentre-server` bump pin 之间，控制台跑的是不传 anchor 的旧宿主代码。可选参数让那段窗口期等价于今天的行为。Rejected: 把行号编回 path 里传（`"src/a.ts:311-330"`）—— path 同时是标签在会话内的唯一键，带上行号会让同一文件的两条不同引用开出两个标签 |
| 6 | **两端同一轮做完**，按 `agentre → agentre-server` 的强制顺序交付 | 用户决定。共享包是唯一实现者，控制台只补 host adapter 的传参 |

## 定位目标（anchor）的形状与流向

**一条定位目标**由起始行、可选终止行、以及一个自增 nonce 构成。行号是 1-based，与链接文本里写的一致。

链接被点下去时：`classifyLink` 已解析出的 `line` / `endLine` 与 relPath 一起交给宿主的 `previewFile`；链接不带行号时不产生定位目标，端口收到的第三参缺席，此后每一层的行为与今天逐字节相同。

宿主（桌面端 store / 控制台的标签 hook）在开或复用标签时把定位目标连同一个自增 nonce 记到**活动标签**上，再由预览面板交给代码视图。同一条链接被重复点击时 path 与行号都不变，但 nonce 每次自增——这就是「重新定位」在数据上的全部表达。

定位目标属于**标签自己**，而不是某个全局的「当前跳转」：每个标签各记各的，关掉标签即随之消失。
面板的正文容器按路径重挂载，因此切走再切回来会重新定位到同一段——这不是缺陷：不带定位目标时
切回来本来也会回到文件开头，落在当初跳到的那一段严格更好。

## 编辑器里的观察结果

**前置条件**：活动标签是代码或文本档（`previewKind` 为 `code`，或 markdown 的 `text` / `split` 档），正文已经读回来并落到编辑器模型上。

**动作**：定位目标到达（新的 nonce）。

**观察结果**：编辑器把起始行滚到视口中央，并把「起始行第 1 列」到「终止行行尾」整段设为当前选区。没有终止行时选区就是起始行整行。

**owned failure paths**：

- **行号越界**（起始行 > 文件行数，或终止行 > 文件行数）：钳到文件末行，照常滚动与选中，不报错、不提示。起始行 < 1 同样钳到第 1 行。
- **正文还没到**（首次打开、或轮次结束后重读）：定位在正文落定之后发生，不在空模型上滚一次再滚一次。
- **Monaco 还没装载好**（`monaco` 为 null）：内容容器本来就留空，定位目标留着，编辑器建好并且正文落定后照常执行一次。
- **正文读失败 / 文件不存在**：走既有的失败态（`2026-09-07-preview-failure-classification.md`），不渲染编辑器，定位目标自然无处落地，不额外产生任何提示。

**不参与定位的档位**：图片档、`session`（工具 diff）档，以及 markdown 的 `render` 档——这三档没有可定位的行，定位目标被忽略，标签照常打开或激活。

## 与既有语义的关系

- **标签的开 / 复用 / 淘汰不受影响。** 带不带行号都是同一次 `openPreview`：文件没开过就开成临时标签，开过就激活既有标签。行号不参与标签的唯一键。
- **markdown 首视图**只在「这次是新开标签」且「带行号」两个条件同时成立时取 `text`；其余情况沿用今天的默认。
- **hover 浮层**照旧显示 `line` / `col` 芯片，不因本轮变化。行范围在浮层里怎么显示不在本轮范围内（今天显示起始行）。
- **`local-external` 与非 allowlist 扩展名**仍然整条走外部打开，`previewFile` 一次都不问——这条分流判据（`rich-link.tsx:147-158`）不动。

## 跨仓库交付顺序（强制）

1. 在 `agentre` 里完成共享包的实现与测试，接上桌面宿主，验证，提交并**推送**。
2. 在 `agentre-server` 里 pin 到那个 revision，把行号接进它自己的 `ServerTranscriptPortDeps.previewFile`（`agentre-server/frontend/src/lib/transcriptPorts.ts:35,80-85`）与标签 hook（`agentre-server/frontend/src/components/session/useFilePreviewTabs.ts`、`SessionDetailView.tsx:419-436`），验证该宿主。
3. 两个仓库各自独立提交。控制台在第 1 步与第 2 步之间保持今天的行为。

## Out of scope

- **侧栏文件面板的行定位。** 那里的路径来自工具调用，不带行号（`frontend/src/components/agentre/chat-context-sidebar/views/use-open-file.ts`）。
- **`git` 档 diff 视图里的定位。** 转录链接恒以 `directory` 模式打开（`transcript-ports-desktop.ts:112-118`），到不了 diff 视图。
- **外部打开（`OpenPath`）带行号跳转。** 仍归「编辑器 URL scheme」设置项这一未来工作。
- **`#L311-L330` 这类 GitHub 风格锚点。** 本轮只认冒号后缀。
- **行范围在 hover 浮层里的显示形态。**

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `link-classify.test.ts` | `:311-330` 解析出 `line` + `endLine`；`:42` / `:42:7` 不产生 `endLine` | 同文件既有的 line/col 用例 |
| `rich-link.test.tsx` | 带行号的 in-cwd 可预览路径把 anchor 交给 `previewFile`；不带行号时第三参缺席；`local-external` / 非 allowlist 仍不问 `previewFile` | `previewFile routing (files.open_action)` 那一组 |
| `code-view.test.tsx` | fake monaco 上断言 `revealLineInCenter` / `setSelection` 的入参；nonce 变化重跑一次；越界钳到末行；正文落定之前不定位 | 同文件既有的 fake monaco 装配 |
| `file-preview-tabs-store.test.ts` | anchor 落到活动标签；重复 `openPreview` 自增 nonce；rehydrate 后 anchor 为 null 且**旧持久化条目不被整条丢弃** | 同文件既有的持久化清洗用例 |
| `file-preview-panel.test.tsx` | markdown 新开标签且带行号 → 首视图 `text`；已开标签不改档位；图片 / `session` 档忽略 anchor | 同文件既有的档位钳制用例 |
| `transcript-ports-open-mention.test.ts` | 桌面端 `previewFile` 把 anchor 透传给 `openPreview` | 同文件既有的两条分流用例 |
| 桌面宿主壳 `components/agentre/__tests__/file-preview-panel.test.tsx` | 壳把活动标签上的 anchor 交给共享面板：anchor 先于挂载存在、anchor 后到已挂载的编辑器、生产入口的 `StrictMode` 双次挂载三种次序 | 同文件既有的真实 store + fake monaco 装配 |
| `agentre-server` 的 `file-preview-tabs.test.ts` / `transcript-preview-entry.test.tsx` | 控制台宿主同样透传 anchor | 两文件既有用例 |

桌面宿主壳那一行是补记的：初版遗漏了它，而包里两层各自有用例、都绿，壳漏传照样绿——这一跳
无人看守，2026-09-08 的运行期排查里它正是最可疑的一环。

自动化覆盖不到的只有一件：**滚动位置与选区在真实 Monaco 上肉眼是否落在预期那一段**（fake monaco 只能断言调用参数）。由一次 `make verify-up` 驱动的运行期观察补上，按 `docs/verification.md` 留证据。

## Open questions

（无）
