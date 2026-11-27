# 接入 Antigravity CLI，经 Gemini 协议供应商跑对话

> Status: Approved
> Owner: desktop app maintainers
> Last updated: 2026-11-25（修正轮：V5 / V15 / 中断后审批卡 / 绑定选项 / 配对提示）

**Objective:** 用户能新建一个 Antigravity 后端，绑定一个 Gemini 协议的 LLM 供应商（比如 huu），在本机或远程执行端用 `agy` 跑多轮对话，审批、中断、工具调用都和现有 ACP 后端一样可用。

**Hard invariant:** Agentre 不创建、不修改、不删除用户全局 `~/.gemini` 下的任何配置文件。真实供应商 key 只出现在 Agentre 网关里，不进入 `agy` 进程的环境变量、参数或配置文件。

## Problem

1. **没有接入 Antigravity 的正路。** 现有 ACP 后端（`TypeACP`）能用 `agy acp` 拉起进程，但握手只记录 `authMethods`，从不调用 `authenticate`（`internal/pkg/agentruntime/runtimes/acp/client.go:154`）。因此 `session/new` 必定返回 `-32000 Authentication required`（2026-11-25 本机实测，agy 1.5.16）。
2. **自定义供应商只能改全局配置。** 按官方文档，BYOK 要在 `~/.gemini/antigravity-cli/settings.json` 写 `modelProvider`、`customModelsConfig`，这会影响用户在终端里直接用的 agy。实测发现 ACP 模式根本不读 `customModelsConfig`：CLI 模式的 `agy models` 能列出自定义模型，ACP 的 `session/new` 返回的 25 个模型全是内置 Gemini，按自定义模型的 label 调 `session/set_config_option` 报 `Unknown model ID`。
3. **网关不认 Gemini 协议。** 本机网关只有 `/v1/messages`、`/v1/responses`、`/v1/chat/completions` 三条路由（`internal/pkg/httpgateway/types.go:24`）。供应商类型也只有 anthropic、openai-chat、openai-response 三种（`internal/model/entity/llm_provider_entity/llm_provider.go:28`）。
4. **key 放 `env_json` 会泄露到服务端。** `env_json` 会经 syncwire 同步到服务端，把供应商 key 写进去等于交给 SaaS 库。

## Actors and user stories

1. 作为 Agentre 用户，我想把 huu 这类同时提供 Gemini 协议入口的中转站配成一个供应商，让 Antigravity 后端用它跑对话，这样不用 Google 账号也能用 agy。
2. 作为同时在终端里直接用 agy 的用户，我希望 Agentre 起的 agy 不改动我的全局配置，我自己用 agy 时行为不变。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 用 `agy acp`（ACP v1，stdio）驱动，复用现有 ACP runtime | 已核实：1.5.16 的 ACP 支持 `loadSession`、`resume`、图片输入、`session/request_permission`、追加系统提示（`_meta.antigravity.systemPrompt.append`）。t3code 同样全程走 ACP。Rejected: `agy -p --output-format stream-json` —— 没有审批回调，stdin 里出现控制消息就以退码 2 退出 |
| 2 | 新增独立后端类型 `antigravity`，不往通用 ACP 后端上加选项 | 认证方式、环境变量构造、供应商匹配都是 agy 专属的，通用 ACP 后端「自带供应商、不参与匹配」的语义保持不变。Rejected: 给 `TypeACP` 加开关 —— 会让通用后端同时承担两套供应商语义 |
| 3 | 新增 `gemini` 供应商类型，网关新增 Gemini 直通路由，agy 只拿会话网关 token | 用户选定方案 A。和 Claude Code / Codex 一致，真实 key 只在网关里。Rejected: key 存在后端自身（`backendcred`）—— 供应商要配两遍、模型要手填；Rejected: key 写进 `env_json` —— 会同步到服务端 |
| 4 | 模型由网关改写请求路径里的模型名决定，不用 `customModelsConfig` | 实测：ACP 模式不读 `customModelsConfig`；agy 请求 `/v1beta/models/<内置模型>:streamGenerateContent`，网关把路径里的模型名改写成 `glm-5.3` 后，整轮对话（含工具调用）成功。Rejected: 写 `customModelsConfig` —— ACP 不生效，还要改全局配置 |
| 5 | 主调用和旁路调用一律改写到会话选定的模型 | 实测：agy 每轮除主模型 `gemini-3.1-pro-preview` 外，还会调一次 `gemini-3.1-flash-lite-preview`；huu 没有这两个模型，不改写就是 503。Rejected: 旁路调用单独映射一个小模型 —— 用户没提这个需求 |
| 6 | 不隔离，用用户真实的 `HOME`，Agentre 不写任何 agy 配置文件 | 用户决定。和 Claude Code / Codex 现状一致（都直接用真实的 `~/.claude`、`~/.codex`）。实测：不写 settings.json，只要 `authenticate("gemini-api-key")` 加两个环境变量就能跑通。Rejected: 影子 HOME —— 要软链镜像整个家目录，git、ssh 有风险；Rejected: t3code 的 `GEMINI_HOME` —— 1.5.16 不认，实测仍写真实 `~/.gemini`，二进制里也没有这个字符串 |
| 7 | 关闭 agy 自动更新（`AGY_CLI_DISABLE_AUTO_UPDATE=1`） | Agentre 拉起的进程不应在会话中途替换用户自己安装的 agy。t3code 同样关闭 |
| 8 | 本期只做 API Key 模式，Google 账号登录另开 | Google 账号登录涉及浏览器授权、贴码回填、额度展示，是一个可独立交付的子系统；用户这一期要的是接供应商 |
| 9 | 上游报错不另判失败，照 agy 的回复显示 | 实机验证（V15）：agy 对上游 503 自行重试后以回复正文报错、`stopReason=end_turn` 结束；用户决定照此显示。Rejected: 按「⚠ agent executor error」文本把这一轮判失败 —— 依赖 agy 文案，属脆弱启发式 |
| 10 | 中断时收起挂起的审批卡，放进本轮修正，通用 ACP 一并适用 | 实机验证发现停止后审批卡仍可点；用户决定一起修。Rejected: 另开一轮 |

## 供应商：`gemini` 类型

- **字段**：和现有供应商类型一样，有 base URL、key、模型列表。base URL 填根地址，比如 `https://huu.dqy.ink`；网关自己拼 `/v1beta/...`。base URL 留空时用 Google 官方端点 `https://generativelanguage.googleapis.com`，和现有类型「留空走官方默认」的做法一致。
- **连通性与模型**：探测和拉取模型都请求 `GET {base}/v1beta/models`，key 放在 `x-goog-api-key` 请求头里。401/403 报 key 无效，网络错误报不可达，和现有类型的探测文案同一套。
- **匹配**：只有 `antigravity` 后端能绑定 `gemini` 供应商。builtin、Claude Code、Codex、Pi 等现有后端都不匹配它，其中 builtin 现在对所有类型都匹配，要改成排除 `gemini`。

## 后端：`antigravity` 类型

- **建后端**：必须绑定一个 `gemini` 供应商。可执行文件默认按 `agy` 在 PATH 上查找，允许用已有的 `cli_path` 按设备覆盖。启动参数固定为 `acp`。
  - 模型绑定的选项里不出现「CLI 自身登录态」：antigravity 没有 CLI 登录这条路，新建时默认就是未选，提示先选一个 gemini 供应商。
  - 按设备的 `cli_path` 覆盖在**对话时**同样生效，本机执行与 agentred 执行都一样：覆盖了路径，就用这个路径启动 agy，不再去 PATH 找。路径指向的文件不存在时，这一轮失败，报错说明找不到 agy 及所用路径，不提 `acpCommand`。
- **探测**：显示 agy 版本。低于 1.5 时报「版本过低，需要 1.5 或更新」；找不到可执行文件时报未安装。已验证版本：1.5.16。
- **启动一轮**（前提：后端已绑定可用的 gemini 供应商）：
  - 环境变量写入 `GEMINI_API_KEY`（会话网关 token）、`GOOGLE_GEMINI_BASE_URL`（本机网关根地址）、`AGY_CLI_DISABLE_AUTO_UPDATE=1`。
  - 先清掉继承来的 Google 凭证类变量，避免它们盖过网关 token：`GEMINI_API_KEY`、`GOOGLE_API_KEY`、`GOOGLE_APPLICATION_CREDENTIALS`、`GOOGLE_CLOUD_PROJECT`、`GOOGLE_CLOUD_LOCATION`、`GOOGLE_CLOUD_QUOTA_PROJECT`、`GOOGLE_GENAI_USE_VERTEXAI`、`AGY_ADC_AUTH`。
  - `antigravity` 后端的 `env_json` 里不允许填 `GEMINI_API_KEY` 和 `GOOGLE_GEMINI_BASE_URL`，保存时被拒绝。这两个键只对 `antigravity` 保留：通用 ACP 后端（比如 `gemini --acp`）照旧可以在 `env_json` 里填 `GEMINI_API_KEY` 来认证（决策 2）。
- **握手**：`initialize` 之后、`session/new` 之前调用 `authenticate("gemini-api-key")`。
  - 对方没有提供 `gemini-api-key` 方法，或 `authenticate` 返回错误：这一轮失败，错误里带 agy 的报错和 stderr 末尾，不再继续发 `session/new`。
- **对话能力**：中断、工具审批、图片输入、上下文窗口上报、会话恢复，全部沿用 ACP runtime 现有行为。
  - 中断一轮时，这一轮里还挂着的审批卡随之收起、不再可点（通用 ACP 后端同样适用）。
  - agy 的审批只有「允许一次 / 拒绝」两个选项（实测），不会出现「总是允许」。
- **模型**：用户在 Agentre 里选的供应商模型就是实际调用的模型。agy 自己的模型、思考档位、模式选择器不显示在 Agentre 里。

## 网关：Gemini 直通路由

- **路由**：匹配 `POST /v1beta/models/{model}:{method}`，`method` 是任意 Gemini 方法（实测用到的是 `streamGenerateContent?alt=sse`）。
- **鉴权**：从 `x-goog-api-key` 请求头取会话 token，也接受 `?key=` 查询参数。token 无效时返回 401，不访问上游。
- **转发**：
  - 上游地址是 `{供应商 base}/v1beta/models/{改写后的模型}:{method}`，原查询参数保留（`key` 除外）。
  - 请求头里的 token 换成供应商的真实 key。
  - 路径里的模型名一律改写成这个 token 绑定的模型。
  - 请求体原样透传；SSE 响应逐块透传，不能缓冲到结束才吐。
- **上游报错**：状态码和响应体原样回给 agy，比如 huu 偶发的 `model_not_found` 和 503。agy 会自己重试，最后把报错写进回复正文并正常结束这一轮（实测）；Agentre 照它的语义显示这段报错，不另外把这一轮判成失败。

## 全局配置约束

- **Agentre 这边**：不在 `~/.gemini` 下创建、修改或删除任何文件。
- **agy 这边**：首次运行会在 `~/.gemini/antigravity-cli/` 下建会话、日志、状态等运行时数据（实测：`conversations/`、`brain/`、`log/`、`conversation_summaries.db`、`jetski_state.pbtxt`、`installation_id`）。这些是 agy 自身的运行状态，不算配置。
- **agy 首次运行时补建的默认配置**：`~/.gemini/config/config.json`、`~/.gemini/config/mcp_config.json` 不存在时，agy 会自己建出默认内容（实测：前者是 `{"plugins":{"swarm":{"enabled":true}}}`，后者是空文件）。这是 agy 自身的首次运行行为，用户自己第一次跑 agy 也会这样，用户确认不算违反本规格的硬约束；Agentre 不做启动前检查，也不拦截。
- **已有配置文件不被改动**：实测在 `antigravity-cli/settings.json`、`config/config.json`、`config/mcp_config.json` 都已存在的情况下，跑完 `authenticate("gemini-api-key")` 和需要审批的命令，三个文件逐字节不变。
- **共用的部分**：用户 `~/.gemini` 里配的 MCP、hooks、权限规则、skills 会对 Agentre 发起的会话生效；Agentre 的会话会出现在用户 agy 的会话历史里。这是不隔离（决策 6）的代价，用户已接受。

## 兼容性

- **远程执行端**：agentred 注册同一个 runtime，自带的网关也有 Gemini 路由。没有这个 runtime 的老版本 agentred 收到这一轮时，现有逻辑会让它失败并报错 `no runtime registered for backend type "antigravity"`（`internal/daemon/handlers/runtime.go:354`），本期不另加预检。
- **协议**：后端类型和供应商类型都是现有字符串字段的新取值，不新增 wire 或 syncwire 字段，不抬协议版本。
- **配对成功提示**：远端设备配对成功后，提示里列出的可选引擎包含 Antigravity。
- **共享前端包与服务端控制台**：后端编辑器、供应商编辑器、品牌图标、i18n 文案里的新类型由 `agentre-ui` 提供。服务端控制台按跨仓顺序更新固定版本号后才能显示这两个新类型。

## Out of scope

- 用 Google 账号或 Gemini Enterprise 登录、额度展示（二期）。
- 影子 HOME 或任何形式的 agy 状态隔离。
- 在 Agentre 里接 agy 的模型、思考档位、模式选择器。
- Agentre 托管安装或更新 agy。
- 旁路调用单独映射模型。
- 把现有 anthropic / openai 类型供应商经协议转换给 agy 用（huu 这类本身就提供 Gemini 入口）。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 后端实体校验 | `antigravity` 只匹配 `gemini`；builtin 等现有后端不匹配 `gemini`；两个新保留键被拒绝 | `agent_backend_entity/kinds_test.go`、`acp_kind_test.go` |
| 环境变量构造 | 写入的三个变量、清掉的继承变量、`env_json` 与会话变量的优先级 | `agentruntime/clienv_test.go` |
| ACP 握手 | antigravity 握手后先 `authenticate("gemini-api-key")` 再 `session/new`；没有该方法或认证失败时这一轮失败、不发 `session/new`；通用 ACP 后端不调用 `authenticate` | `runtimes/acp/fake_agent_test.go`、`negotiation_test.go` |
| 网关 Gemini 路由 | token 校验、key 替换、`key` 查询参数剥离、路径模型改写（含旁路模型）、SSE 逐块透传、上游错误原样回传 | `httpgateway/llmforward_test.go` |
| gemini 供应商探测 | `GET /v1beta/models` 的成功、401、网络错误分支 | `pkg/llmurl` 探测测试 |
| agy 版本探测 | 版本解析与 1.5 门槛、未安装 | `cliprober/probe_test.go` |
| 对话时的 `cli_path` 覆盖 | 本机与 agentred 两条执行路径上，按设备覆盖的路径进入 antigravity 的启动参数；路径不存在时报错不提 `acpCommand` | 修正轮新增；Claude Code / Codex 覆盖的现有测试 |
| 模型绑定选项 | antigravity 不提供「CLI 自身登录态」，新建默认未选 | `antigravity-backend.test.tsx` |
| 中断收起审批卡 | 挂起审批时中断，审批卡收起、不可再点（antigravity 与通用 ACP） | 修正轮新增，先写失败的回归测试 |

无法自动化的部分由收尾时的实机验证覆盖：
- 本机装 agy，绑定 huu 跑一轮带工具调用和审批的对话；
- 跑前跑后对比 `~/.gemini` 下已有配置文件的哈希，确认逐字节不变；
- 远程执行端（本分支编的 agentred）上同样跑一轮，且不靠 PATH，只靠按设备的 `cli_path` 覆盖找到 agy；
- 若用户届时完成 dev 控制台登录与配对，再经服务端中继跑一轮。

## Open questions
