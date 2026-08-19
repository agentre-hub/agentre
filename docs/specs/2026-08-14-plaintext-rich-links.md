# AI 纯文本路径与链接自动富链接

> Status: Approved
> Owner: chat experience / frontend
> Last updated: 2026-08-14

**Objective:** 让聊天转录中的 AI 纯文本 URL、绝对路径、相对路径和常见文件名在没有 Markdown `[说明](目标)` 语法时也能被识别为现有富链接，并保持原文、代码排版与既有链接安全边界不变。

**Hard invariants:**

1. 已有 Markdown 链接和图片优先，自动识别不得生成嵌套链接、重复图标或改变其目标。
2. `javascript:`、`data:` 和其它未列入既有白名单的协议仍不可点击；本轮不放宽 `href` 安全边界。
3. fenced code block 内永不自动生成链接；行内代码只有整段内容是一个可识别目标时才转换，并保留代码外观。
4. 自动识别只发生在渲染层，不改写或迁移已持久化的消息正文。
5. 流式消息与定稿消息共用同一识别规则；现有“只重解析活跃尾部、已定稿段保持稳定”的增量渲染特性不得回归。
6. 本轮不新增后端、Wails、wire、数据库或迁移能力；点击与失败反馈复用现有 `RichLink` 合约。

## Problem

1. **纯文本本地路径不会进入富链接路径。** `frontend/packages/agentre-ui/src/lib/link-classify.ts` 当前只分类 URL、`file://`、POSIX 绝对路径和 Windows 绝对路径；`frontend/packages/agentre-ui/src/lib/link-classify.test.ts` 明确把 `internal/foo.go` 断言为 `unknown`。因此 `frontend/src/chat.tsx:42`、`README.md` 等 AI 常见输出无法解析为会话 cwd 下的文件目标。
2. **富链接只接管已经解析出的 Markdown `<a>`。** `frontend/packages/agentre-ui/src/transcript/markdown-text.tsx` 仅在 React Markdown 的 `a` 组件映射中调用 `RichLink`；普通文本节点原样渲染。用户已确认 AI 输出可能只是文本，不保证使用 `[xx](xx)` 形式。
3. **AI 常用行内代码表达路径。** 现有内联装饰器为避免污染源码而整体跳过 `code` / `pre` 子树；若原样复用于路径识别，`` `README.md` `` 和 `` `frontend/src/chat.tsx:42` `` 仍不可点击。用户已确认行内代码路径应识别，而 fenced code block 必须保持不变。
4. **宽泛正则会造成明显误识别风险。** 文件名与无协议域名、相对路径与日期/分数、带空格路径与普通句子存在歧义。用户已确认采用严格规则：无协议域名不识别，带空格路径必须由引号或反引号包裹，单文件名只识别可信文件类型。

## Actors and user stories

1. 作为查看 AI 回复的用户，我希望 `frontend/src/chat.tsx:42`、`README.md` 或绝对文件路径即使没有 Markdown 链接语法也可直接点击，以减少手动复制和定位文件的操作。
2. 作为查看技术说明的用户，我希望裸 `http(s)` / `www` 等允许的 URL 保持可点击，并继续由系统浏览器打开。
3. 作为阅读代码和命令输出的用户，我希望 fenced code block 保持逐字原样，不因包含类似路径的文本而出现大量错误链接。
4. 作为阅读 AI 常见行内代码路径的用户，我希望整段为路径的行内代码既保留代码样式，又具备富链接的悬浮详情与点击能力。
5. 作为阅读历史会话或流式回复的用户，我希望同一段内容在流式结束前后采用同一识别规则，不需要迁移历史消息。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 在 Markdown 已解析后的结构化文本节点上做自动识别，再交给现有 `RichLink` 渲染 | 可跳过已有链接、图片与 fenced code block，并保留原始 Markdown 结构。Rejected: 在解析前用正则把原文改写成 `[text](href)`——容易破坏转义、表格、图片、半截流式 Markdown 和已有链接 |
| 2 | 识别采用严格、确定性的词法规则，不查询文件系统确认存在性 | 渲染同步稳定，本机、历史与流式消息不会因异步 stat 发生跳变。Rejected: 每个候选路径调用后端确认——会产生大量 IPC、扩大远端语义和失败状态，超出当前需求 |
| 3 | 普通文本与行内代码分开处理：普通文本可切出多个目标；行内代码仅在整段内容是一个目标时转换 | 用户确认 AI 常用反引号路径；整段门槛可避免把 `` `go test ./...` `` 等命令参数拆成链接。Rejected: 继续跳过全部 `code`——遗漏高频 AI 输出；Rejected: 拆分行内代码——命令和表达式误识别风险过高 |
| 4 | 单文件名仅在后缀属于项目现有可预览文件类型时识别；未知 `foo.bar` 保持文本 | 用户要求支持 `README.md` / `main.go:42`，同时接受严格误识别控制。Rejected: 任意“含点名称”均识别——会把版本、域名和普通标识符误作文件 |
| 5 | 无协议域名不识别；URL 继续限定为 `http://`、`https://`、`www.`、`mailto:`、`tel:` | 用户确认文件名优先，避免 `README.md` 与 `example.com` 的不可可靠歧义。Rejected: 自动补 `https://`——需要新增域名/文件优先级，并会扩大当前 URL 合约 |
| 6 | 带空格路径仅在单/双引号或反引号完整包裹时识别；未包裹文本不猜测 | 用户确认该边界；它能让目标终点确定。Rejected: 从首个路径信号贪婪匹配到句末——会吞掉普通语言文本与标点 |
| 7 | 自动生成的目标复用 `RichLink` 的分类、悬浮详情、复制、图标、点击与 toast，不建立第二套链接 UI | 现有组件已拥有 URL、项目内/外路径和 `:line[:col]` 语义。Rejected: 新建纯文本专用链接组件——会复制安全与交互规则并导致行为漂移 |
| 8 | 自动识别作为 Markdown 内建渲染能力，对流式和定稿路径同时生效，并与调用方已有 mention decorator 组合 | `MarkdownInner` 是两条渲染路径的共同边界；单一规则避免流式结束后链接出现/消失。Rejected: 只在定稿消息装饰——流式期间行为不一致 |

## Recognition and precedence

Markdown 解析产生已有链接、图片、普通文本、行内代码和块级代码后，自动识别按以下优先级工作：

1. 已有 Markdown 链接与图片保持原样，其后代文本不再扫描。
2. fenced code block 及其后代文本保持原样。
3. 行内代码去掉外层代码标记后的完整内容若恰好是一个允许目标，则整体成为富链接；否则整个行内代码保持原样，不在内部切段。
4. 普通文本节点可切分成原始文本与多个目标，每个目标分别成为富链接；未命中部分逐字保留。
5. 调用方已有的 mention 等内联 token 与自动链接只消费各自命中的文本范围；已生成 token 不被再次扫描，也不产生嵌套交互元素。

允许目标包括：

- 既有 URL：`https://example.com/a`、`http://example.com`、`www.example.com`、`mailto:a@example.com`、`tel:+1234`；
- file URL：`file:///Users/me/project/main.go`；
- POSIX 绝对路径：`/Users/me/project/main.go`；
- Windows 绝对路径：`C:\work\project\main.go`；
- 显式相对路径：`./docs/guide.md`、`../README.md`；
- 带文件后缀的相对路径：`frontend/src/chat.tsx`；
- 明确的多段目录路径：至少有两个路径分隔符，或以分隔符结尾，并且不能是纯数字日期/分数，也不能是「各段均为单字符、末段无扩展名且不以分隔符结尾」的枚举形态（如 `a/b/c`、`A/B/C`），例如 `frontend/src/components`、`frontend/src/`；
- 单文件名：后缀属于项目现有可预览文件类型，例如 `README.md`、`main.go`、`package.json`；
- 上述文件或路径末尾可带既有 `:line` / `:line:column`，例如 `main.go:42`、`frontend/src/chat.tsx:42:7`。

明确不识别：

- `example.com`、`github.com/owner/repo` 等没有协议且没有 `www.` 的域名形态；
- `docs` 等没有后缀、没有分隔符的单目录名；
- 未命中可信文件类型的单段 `foo.bar`；
- `2026/08/14`、`1/2` 等纯数字日期或分数；
- `a/b/c`、`A/B/C` 等全单字符多段枚举（各段均为单字符、末段无扩展名且不以分隔符结尾，例如「改动 a/b/c 三处」）；
- 没有 cwd 时出现的相对路径或单文件名；
- `javascript:`、`data:` 和其它未允许协议；
- `#L42` 等本轮未纳入的行号语法。

## Delimiters, spaces, and visible text

普通文本中的目标以空白或明确的外围标点为边界。紧随目标的中英文句号、逗号、分号、冒号、感叹号、问号和闭合括号不进入目标；属于 `:line[:column]` 的数字冒号例外。外围单引号或双引号保留为普通可见文本，只有其内部内容成为链接。

带空格路径必须完整位于配对的单引号、双引号或行内反引号中，例如 `"docs/My Guide.md"` 和 `` `docs/My Guide.md` ``。未包裹的 `docs/My Guide.md` 不自动猜测，也不得只链接其中一段制造误导。

自动转换只改变交互渲染，不改变用户看见的目标文字；路径大小写、分隔符、百分号编码和 `:line[:column]` 后缀继续由既有路径分类规则处理。外围引号仍可见，行内代码继续使用现有等宽字体、背景和间距。

## Target resolution and interaction

当会话存在 cwd 时，相对路径和单文件名以该 cwd 解析为绝对目标，再进入现有项目内路径分类；悬浮详情显示项目根、相对路径和行列信息。会话没有 cwd 时，这两类候选保持普通文本，不生成相对浏览器 href。

绝对路径、Windows 路径和 `file://` 继续使用现有项目内/项目外分类。路径点击复用 `RichLink` 当前的系统 `OpenPath` 行为，URL 点击复用系统浏览器行为；复制、文件/目录图标、打开提示和打开失败 toast 均保持现状。自动识别不预先验证目标是否存在，目标不存在或系统无法打开时由现有失败反馈告知用户，消息正文和链接仍保留。

本轮不改变远端会话的文件打开/预览路由；自动生成路径链接与手写 Markdown 路径链接使用同一 `RichLink` 合约，不新增远端读取或侧栏预览分支。

## Streaming, compatibility, and accessibility

流式消息的活跃尾部每次按当前完整文本重新识别：候选尚未形成完整文件后缀或完整包裹边界时保持文本，后续 chunk 补全后才成为链接。已经进入稳定段的内容保持现有 memo 行为，不因后续 chunk 重复解析。

定稿消息、历史消息与新消息不区分存储版本；相同正文在相同 cwd 下产生相同链接结果。已有 Markdown 链接继续优先，旧消息无需数据迁移。

生成的目标使用原生 anchor 语义和 `RichLink` 既有键盘焦点行为；行内代码链接仍可 Tab 聚焦。链接图标为装饰性图标，目标文本继续提供可访问名称。未识别文本不获得误导性的焦点或点击样式。

## Failure and security boundaries

自动识别器只把白名单 URL 和确定的本地路径候选交给 `RichLink`。危险或未知 scheme、无 cwd 相对路径、歧义文本保持纯文本，不产生可导航 DOM href。已有 Markdown href 仍经过当前 URL transform 白名单，不因纯文本识别而放宽。

路径识别不是文件读取授权，也不读取消息中指向的文件。唯一副作用发生在用户主动点击后，由现有 `OpenPath` 或系统浏览器调用承担；打开失败沿用现有 toast。自动识别不得记录、上传或额外持久化消息中的本地绝对路径。

## Out of scope

- 无协议域名自动补 `https://`。
- fenced code block、终端输出组件或本地命令 xterm 的路径识别。
- 未包裹带空格路径的猜测与文件系统存在性探测。
- `#L42`、`(line 42)` 等新增行号语法。
- 点击路径后直接打开会话文件预览侧栏、编辑器定位或远端文件读取。
- 改变现有手写 Markdown 链接、图片、远程图片或本地 Markdown 图片规则。
- 后端、Wails API、wire、数据库、迁移或持久化格式修改。
- 新增用户设置或识别开关。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 纯文本目标 tokenizer | 普通 URL、绝对路径、显式相对路径、多段路径、可信单文件名、`:line[:column]`、中英文外围标点和配对引号；日期、分数、全单字符多段枚举（`a/b/c`）、未知 `foo.bar`、无协议域名、无 cwd 相对路径不命中 | `frontend/packages/agentre-ui/src/lib/link-classify.test.ts` 的路径与 URL 等价类 |
| 路径分类 | 相对路径和单文件名在有 cwd 时解析为项目内绝对目标，Windows 分隔符与 file URL 保持既有行为，无 cwd 时不伪造相对 href | `frontend/packages/agentre-ui/src/lib/link-classify.test.ts` |
| `MarkdownText` 组件 | 纯文本中的多个目标渲染为 `RichLink`；已有 Markdown 链接不嵌套；外围标点/引号和原文可见文本不变；点击相对路径时 `OpenPath` 收到解析后的绝对路径 | `frontend/packages/agentre-ui/src/transcript/markdown-text.test.tsx`、`frontend/packages/agentre-ui/src/transcript/rich-link.test.tsx` |
| 行内代码与 fenced code block | 整段为路径的行内代码可点击且保留 code 样式；混合命令的行内代码和 fenced code block 不转换 | `frontend/packages/agentre-ui/src/transcript/markdown-text.test.tsx` 的 code block 与 decorator 测试 |
| 内联装饰器组合 | mention token 与自动链接可出现在同一文本中，彼此不吞文本、不嵌套交互节点；已有 token 不再次扫描 | `frontend/packages/agentre-ui/src/chat-input/mentions/transcript.test.tsx` |
| `StreamingMarkdown` | 目标在 chunk 补全后出现，半截候选不破坏 Markdown；稳定段不因尾部追加退化为全量重解析 | `frontend/packages/agentre-ui/src/lib/streaming-markdown.test.ts` 与 `markdown-text.tsx` 的 memo 边界 |
| 安全回归 | `javascript:` / `data:` / 未知 scheme 不生成自动链接；已有 Markdown href 白名单结果不变 | `frontend/packages/agentre-ui/src/transcript/markdown-text.test.tsx` 的 URL whitelist 测试 |
| 前端类型与静态门禁 | React 组件类型、现有 transcript typography token 和 lint 规则不回归 | `pnpm exec tsc -b --noEmit`、相关 guard tests、frontend lint |

链接在真实对话流中的视觉密度、浅色/深色主题下的可读性，以及带中文标点的实际 AI 回复可由收尾 runtime observation 补充；自动化测试负责识别、DOM 结构、点击目标和安全边界，不以截图替代这些行为测试。

## Relevant links

- 富链接渲染：`frontend/packages/agentre-ui/src/transcript/rich-link.tsx`
- 路径与 URL 分类：`frontend/packages/agentre-ui/src/lib/link-classify.ts`
- Markdown 渲染与内联装饰接缝：`frontend/packages/agentre-ui/src/transcript/markdown-text.tsx`
- 流式 Markdown 切分：`frontend/packages/agentre-ui/src/lib/streaming-markdown.ts`
- 可信可预览文件类型：`frontend/packages/agentre-ui/src/lib/previewable.ts`
- 前端约束：[`../frontend.md`](../frontend.md)
- 测试设计：[`../testing.md`](../testing.md)
