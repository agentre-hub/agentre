<p align="right">
<a href="../README.md">English</a> | <a href="./README_zh.md">中文</a>
</p>

<h1 align="center">
<img src="../build/appicon.png" width="128" height="128"/><br/>
Agentre
</h1>

<p align="center">本地优先的桌面工作台，用来统一协调 Claude Code、Codex 和其他 AI 编码 Agent，覆盖项目、会话与远端机器。</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go">
  &nbsp;
  <img src="https://img.shields.io/badge/React-19-61DAFB?style=for-the-badge&logo=react&logoColor=white" alt="React">
  &nbsp;
  <img src="https://img.shields.io/badge/Wails-v2-EB4034?style=for-the-badge&logo=wails&logoColor=white" alt="Wails">
  &nbsp;
  <img src="https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey?style=for-the-badge&logo=windows&logoColor=white" alt="平台">
</p>

## 关于

AI 编码工作已经很难塞进一个终端标签页里。真实的一天里，你可能让一个 Agent 修后端行为，让另一个 Agent review 前端改动，让第三个 Agent 查日志，还有一个 Agent 正等你审批工具调用。

Agentre 把这套工作流放进一个桌面应用。每个 **Agent** 都有自己的角色、头像、系统提示词、技能和后端引擎。Agent 归属到 **部门**，在 **项目** 里工作，并且可以并行运行多个 **会话**。应用的重点是让你快速看清谁在运行、谁在等待、下一步该切到哪里。

**如果觉得有用，求个 Star ⭐ 这是对我们最大的支持！**

## 你能用它做什么

- **并排运行多个编码 Agent**：前端、后端、Reviewer、发布、运维等角色可以同时工作，每个角色保留自己的上下文。
- **按 Agent 选择引擎**：每个 Agent 都可以单独使用 Claude Code、Codex 或内置引擎，但交互方式保持一致。
- **按项目组织工作**：围绕代码库或目标聚合会话、成员和 Agent，最近的工作能快速扫清楚。
- **用命令面板快速切换**：通过 `⌘K` 新开会话、跳转会话、切换项目、触发 Agent 动作。
- **在应用内审查工具活动**：文件编辑、Shell 命令、MCP 调用、权限请求、ask-user 提问都会以明确卡片呈现。
- **把会话跑到另一台机器**：局域网内配对 `agentred` daemon，把 Agent 工作放到远端开发机执行，审批仍然回到桌面端。
- **管理长任务状态**：消息排队、活跃会话提示、中断/继续、等待状态，让多个并行任务更容易监督。

## 应用界面

| 区域 | 用途 |
| ---- | ---- |
| **对话** | 驱动 Agent 会话、查看工具调用、审批动作、排队追问、继续被中断的任务。 |
| **看板** | 用四列跟踪任务，按项目子树收窄、筛选、就地编辑；任务上记着打算交给哪个 Agent、在哪台机器上、用什么模型。 |
| **组织** | 用部门和子部门管理 Agent，配置负责人、颜色和角色资料。 |
| **Hooks** | 把 Webhook、通知、定时器、消息等外部触发路由到 Agent 工作流。 |
| **设置** | 配置 Agent 后端、远端设备、项目成员和会话权限模式。 |

## 核心概念

| 概念 | 含义 |
| ---- | ---- |
| **Agent** | 角色 + 头像 + 系统提示词 + 技能 + 后端引擎；一个 Agent 可以运行多个会话。 |
| **部门** | Agent 的组织容器，支持多层嵌套，可设置负责人和主题色。 |
| **会话** | 一次对话或任务执行，状态包括 `running`、`waiting`、`idle`。 |
| **项目** | 围绕某个代码库或目标的工作范围，绑定成员、Agent 和会话。 |
| **任务** | 看板上的一张卡：落在四列之一，可挂在项目下，带标签，并记着打算交给哪个 Agent、在哪台机器上、用什么模型。 |
| **Hook** | 外部事件入口，可通过路由规则把工作派发给 Agent。 |

## 远端执行

Agentre 使用 `agentred` companion daemon，可以在局域网内另一台 macOS、Linux 或 Windows 机器上运行会话。发布版本同时提供 amd64 与 arm64 架构。

在 macOS 或 Linux 上安装最新版本：

```bash
curl -fsSL https://github.com/agentre-hub/agentre/releases/latest/download/install.sh | sh
agentred --version
agentred service install --start
agentred service status
```

在 Windows PowerShell 中安装最新版本（无需管理员终端）：

```powershell
irm https://github.com/agentre-hub/agentre/releases/latest/download/install.ps1 | iex
agentred --version
agentred service install --start
agentred service status
```

然后连接桌面应用：

1. 打开 **设置 → Remote devices**，配对这台 `agentred` daemon。
2. 打开 **设置 → Agent backends**，创建后端，并把运行设备设为刚配对的机器。
3. 在 **项目 → 设置 → 成员** 中添加 Agent，让它使用该后端，并填写远端机器上的工作路径。
4. 从命令面板发起对话。会话实际在远端运行，桌面应用仍然显示工具审批、提问、状态和输出。

## 桌面工作流

- **56px 图标栏**：对话、看板、组织、Hooks、设置。
- **Agent 会话列表**：置顶 Agent、活跃会话状态点、行内会话数。
- **项目会话视图**：按项目扫描最近工作，而不是只能按 Agent 查找。
- **权限模式选择**：会话开始改文件或运行工具前，先明确自主权限。
- **行内 subagent / 工具卡片**：Shell、文件、MCP 活动不用读原始日志也能审查。
- **键盘优先导航**：快速穿梭于 Agent、项目和会话之间。

## 开发

**前置依赖：** [Go 1.26+](https://go.dev/)、[Node.js 22+](https://nodejs.org/) + [pnpm](https://pnpm.io/)、[Wails v2 CLI](https://wails.io/docs/gettingstarted/installation)。

```bash
make install-deps    # 安装前端依赖
make dev             # 开发模式（热重载）
make test            # 后端 Go 测试 + 前端 Vitest
make build           # 构建当前平台的生产版本
make install         # 安装应用包（macOS: /Applications/Agentre.app）
```

安装到其它 macOS 目录：

```bash
make install MACOS_APP_INSTALL_DIR="$HOME/Applications"
```

## 参与贡献

欢迎提 Issue 和 PR。请从[贡献指南](./CONTRIBUTING_ZH.md)（[English](../CONTRIBUTING.md)）开始——它涵盖环境搭建、工程规则、提交规范和 PR 检查清单。

## 开源许可

基于 [GPLv3](../LICENSE) 协议开源。
