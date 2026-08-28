<p align="right">
<a href="../CONTRIBUTING.md">English</a> | <a href="./CONTRIBUTING_ZH.md">中文</a>
</p>

# Agentre 贡献指南

感谢你有兴趣参与贡献！Agentre 是一个 Wails v2 桌面应用（Go 1.26 后端 + React 19 / TypeScript 前端），用于统一协调 AI 编码 Agent。欢迎提 Issue、提 PR、改进文档。

详细的工程规则在 [AGENTS.md](../AGENTS.md) 和 [docs/](./) 中；本文做摘要并链接到细节。

## 贡献方式

- **报告 Bug** — 在 GitHub 提 Issue，附上操作系统、应用版本、复现步骤，以及（如果可能）相关日志。日志和本地数据库的位置见 [debugging.md](./debugging.md)。
- **提出新功能** — 先开 Issue 描述使用场景，在投入写代码之前我们可以先讨论设计。
- **提交 Pull Request** — 见下面的工作流和检查清单。
- **改进文档** — 贡献者文档有自己的纪律；改动 `AGENTS.md` / `docs/*` 之前先读 [documentation.md](./documentation.md)。

## 开发环境

**前置依赖：** [Go 1.26+](https://go.dev/)、[Node.js 22+](https://nodejs.org/) + [pnpm](https://pnpm.io/)（运行 E2E 时需 Node 24+）、[Wails v2 CLI](https://wails.io/docs/gettingstarted/installation)。

```bash
make install-deps    # 安装前端依赖
make dev             # 开发模式（热重载）
make check           # lint + test
```

> 更多命令（聚焦测试、mock、`agentred` daemon、e2e）见 [AGENTS.md](../AGENTS.md)。

## Pull Request 流程

Agentre 采用标准的 GitHub fork 协作模式：

1. **Fork** 本仓库到你的 GitHub 账号，然后克隆你的 fork：

   ```bash
   git clone https://github.com/<your-username>/agentre.git
   cd agentre
   git remote add upstream https://github.com/agentre-hub/agentre.git
   ```

2. **基于 `main` 开新分支**，用有描述性的名字，例如 `feat/...` 或 `fix/...`：

   ```bash
   git fetch upstream
   git checkout -b feat/my-feature upstream/main
   ```

3. **进行修改**，遵循下面的工程规则和提交规范。

4. **推送到你的 fork 并向上游 `main` 发起 PR**：

   ```bash
   git push -u origin feat/my-feature
   ```

   每个 PR 聚焦一件事。描述里说明改了什么、为什么改，有关联 Issue 就附上链接。

5. **响应评审意见**，用后续提交跟进——评审会以本指南中的规则为准。

## 写代码之前

先读 [AGENTS.md](../AGENTS.md)：其中的 **Development Conventions（必读）** 是唯一、完整的索引。下表只是最常用的入口，不是第二份穷举清单：

| 文档 | 内容 |
| ---- | ---- |
| [AGENTS.md](../AGENTS.md) | 基本规则：硬性约束、SOLID、高内聚低耦合、关键事实。 |
| [architecture.md](./architecture.md) | 仓库布局、分层约定、存储路径、数据库迁移。 |
| [develop.md](./develop.md) | TDD/BDD 循环、修 Bug 纪律、代码风格、强制规则、提交规范、CI 闸门。 |
| [testing.md](./testing.md) | 测试如何设计、测试栈、守卫测试、什么不该写。 |
| [observability.md](./observability.md) | 日志入口、级别、埋点位置、字段格式。 |
| [frontend.md](./frontend.md) | shadcn 组件约定、i18n、格式化和 lint。 |

不可妥协的几条，简述如下：

1. **严格 TDD：Red → Green → Refactor。** 没有失败的测试就不写实现代码。新功能从 BDD 风格的行为规格开始（主路径 + 至少一个边界/错误用例）。
2. **先证明 Bug 存在再修。** 先写回归测试，看着它以正确的原因失败，再动手修。修产生坏值的源头——不要在每个消费方加防御代码。
3. **保持 diff 聚焦。** 只改任务需要的文件。不顺手重构、不批量改名、不跑格式化、不做无关清理——它们会掩盖真正的改动并破坏 `git bisect`。
4. **遵守分层。** 依赖单向流动：`internal/app → service → repository → model/entity`。service 只依赖 repository **接口**；新 repository 测试用 sqlmock，新 service 测试用 mockgen repository mock——都不连真实数据库（遗留的 service sqlmock 测试不是先例）。
5. **前端可见文案必须走 i18n。** 新增可见文本用 `t(...)`，并同时更新 `zh-CN` 和 `en` 两份语言文件；表单控件统一用 `@agentre-hub/agentre-ui` 导出的 shadcn 基础组件。
6. **迁移只追加。** 新迁移加到 `migrationList()` 末尾；禁止修改已有迁移。

如果你的任务和这些规则冲突，请停下来在 Issue 或 PR 里提出，不要自行绕过。

## 提交规范

提交信息以 **gitmoji 字符**开头（直接写 emoji 本身，不写 `:shortcode:`），后跟一个空格和改动描述，不需要模块/scope 前缀：

```text
✨ add chat tab drag reorder
🐛 fix session cwd restore
✅ test provider missing API key
📝 update contributing guide
```

常用选项：✨ 新功能 · 🐛 修 Bug · ⚡️ 性能 · ♻️ 重构 · 🎨 UI · 📝 文档 · ✅ 测试 · 🔧 配置。完整对照表和 Issue 引用规则见 [develop.md](./develop.md)。

## Pull Request 检查清单

开 PR 之前请确认：

- [ ] 测试先于实现编写，覆盖主路径 + 至少一个边界/错误用例。
- [ ] `make check` 通过（golangci-lint + ESLint + 后端 Go 测试 + 前端 Vitest）。
- [ ] diff 只包含任务范围内的改动。
- [ ] 新增的前端可见文案在 `frontend/src/i18n/locales/zh-CN/` 和 `en/` **两边**都有翻译，且落在同一个按域拆分的模块文件里。
- [ ] 提交信息符合上面的 gitmoji 规范。
- [ ] 如果改了贡献者文档，遵循了 [documentation.md](./documentation.md)（链接可解析、事实已对照拟提交的暂存树核验）。

## 开源许可

Agentre 基于 [GPLv3](../LICENSE) 协议开源。提交贡献即表示你同意你的贡献以相同条款授权。
