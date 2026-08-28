<p align="right">
<a href="./CONTRIBUTING.md">English</a> | <a href="./docs/CONTRIBUTING_ZH.md">中文</a>
</p>

# Contributing to Agentre

Thanks for your interest in contributing! Agentre is a Wails v2 desktop app (Go 1.26 backend + React 19 / TypeScript frontend) for coordinating AI coding agents. Issues, pull requests, and documentation improvements are all welcome.

The detailed engineering rules live in [AGENTS.md](./AGENTS.md) and [docs/](./docs); this guide summarizes them and links to the details.

## Ways to Contribute

- **Report a bug** — open a GitHub issue with your OS, app version, steps to reproduce, and (if possible) relevant logs. [docs/debugging.md](./docs/debugging.md) explains where logs and the local database live.
- **Propose a feature** — open an issue describing the use case first, so we can discuss the design before you invest time in code.
- **Submit a pull request** — see the workflow and checklist below.
- **Improve documentation** — contributor docs have their own discipline; read [docs/documentation.md](./docs/documentation.md) before editing anything under `AGENTS.md` / `docs/*`.

## Development Setup

**Prerequisites:** [Go 1.26+](https://go.dev/), [Node.js 22+](https://nodejs.org/) with [pnpm](https://pnpm.io/) (Node 24+ when running E2E), and the [Wails v2 CLI](https://wails.io/docs/gettingstarted/installation).

```bash
make install-deps    # install frontend dependencies
make dev             # development mode with hot reload
make check           # lint + test
```

> More commands (focused tests, mocks, the `agentred` daemon, e2e) are listed in [AGENTS.md](./AGENTS.md).

## Pull Request Workflow

Agentre follows the standard GitHub fork model:

1. **Fork** the repository to your GitHub account and clone your fork:

   ```bash
   git clone https://github.com/<your-username>/agentre.git
   cd agentre
   git remote add upstream https://github.com/agentre-hub/agentre.git
   ```

2. **Create a branch off `main`** with a descriptive name such as `feat/...` or `fix/...`:

   ```bash
   git fetch upstream
   git checkout -b feat/my-feature upstream/main
   ```

3. **Make your changes**, following the ground rules below and the commit style.

4. **Push the branch to your fork and open a PR** against the upstream `main`:

   ```bash
   git push -u origin feat/my-feature
   ```

   Keep each PR focused on one task. In the description, explain what changed and why, and link the related issue if there is one.

5. **Respond to review feedback** with follow-up commits — reviewers check against the rules in this guide.

## Before You Write Code

Start with [AGENTS.md](./AGENTS.md): its **Development Conventions (required reading)** section is the canonical, complete index. These are the most common entry points, not a second exhaustive list:

| Document | What it covers |
| -------- | -------------- |
| [AGENTS.md](./AGENTS.md) | The ground rules: hard constraints, SOLID, high cohesion / low coupling, key facts. |
| [docs/architecture.md](./docs/architecture.md) | Repository layout, layering conventions, storage paths, migrations. |
| [docs/develop.md](./docs/develop.md) | TDD/BDD loop, fix discipline, code style, enforced rules, commit style, CI gate. |
| [docs/testing.md](./docs/testing.md) | How tests are designed, the test stack, guard tests, what not to write. |
| [docs/observability.md](./docs/observability.md) | Logging entry point, levels, where to instrument, field format. |
| [docs/frontend.md](./docs/frontend.md) | shadcn component conventions, i18n, formatting and lint. |

The non-negotiables, in short:

1. **Strict TDD: Red → Green → Refactor.** No implementation code without a failing test first. New features start from a BDD-style behavior spec (happy path + at least one boundary/error case).
2. **Prove a bug before fixing it.** Write a regression test, watch it fail for the right reason, then patch. Fix the producer of a bad value — don't add guards at every consumer.
3. **Keep the diff focused.** Touch only the files the task requires. No drive-by refactors, rename sweeps, formatter passes, or unrelated cleanups — they bury the real change and break `git bisect`.
4. **Respect the layering.** Dependencies flow one way: `internal/app → service → repository → model/entity`. Services depend on repository **interfaces**; new repository tests use sqlmock and new service tests use mockgen repository mocks — neither touches a real database (legacy service sqlmock tests are not precedent).
5. **Frontend UI copy goes through i18n.** New visible text uses `t(...)` and updates both `zh-CN` and `en` locale files; form controls use shadcn `@/components/ui/*`.
6. **Migrations are append-only.** Add new migrations to the end of `migrationList()`; never modify an existing one.

If your task seems to conflict with one of these rules, stop and raise it in the issue or PR instead of working around it.

## Commit Style

Commit messages start with a **gitmoji glyph** (the emoji character itself, not a `:shortcode:`), then a space and the description — no scope prefix:

```text
✨ add chat tab drag reorder
🐛 fix session cwd restore
✅ test provider missing API key
📝 update contributing guide
```

Common choices: ✨ feature · 🐛 bug fix · ⚡️ performance · ♻️ refactor · 🎨 UI · 📝 docs · ✅ tests · 🔧 config. The full table and the rules for issue references are in [docs/develop.md](./docs/develop.md).

## Pull Request Checklist

Before opening a PR, confirm:

- [ ] Tests were written first and cover the happy path plus at least one boundary/error case.
- [ ] `make check` passes (golangci-lint + ESLint + backend Go tests + frontend Vitest).
- [ ] The diff only contains changes in scope for the task.
- [ ] New visible frontend copy is translated in **both** `frontend/src/i18n/locales/zh-CN/` and `en/`, in the same domain module file on each side.
- [ ] Commits follow the gitmoji style above.
- [ ] If you changed contributor docs, you followed [docs/documentation.md](./docs/documentation.md) (links resolve, facts verified against the proposed staged tree).

## License

Agentre is released under [GPLv3](./LICENSE). By contributing, you agree that your contributions are licensed under the same terms.
