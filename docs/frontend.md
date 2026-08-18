# Frontend Conventions

React 19 + TS + Vite + Tailwind v4. Wails bindings are generated from `internal/app` into `frontend/wailsjs/` (gitignored).

## UI Components

**Frontend form controls go uniformly through shadcn `@/components/ui/*`.**

- Use `Select / SelectTrigger / SelectContent / SelectItem / SelectValue` for dropdowns (see `agent-backends.tsx` / `llm-providers.tsx`). Native `<select>` is **forbidden**.
- Input / Switch / Dialog / Button, etc. all use the wrappers in the ui directory.
- A native `<input type="radio">` may be kept when shadcn does not provide a styled equivalent, but before adding one, check whether the ui directory already has an equivalent component.

**Rationale:** theme color / dark mode / accessibility / keyboard interaction are all handled uniformly in the ui layer; native tags bypass the design tokens and end up producing two visual styles on the same page.

Before adding a component, check whether `frontend/src/components/ui` and `frontend/src/components/agentre` already have a primitive.

> **Design system →** the visual language those components express — color tokens (full light/dark values), the 16-color agent palette & run-status system, theming, the desktop window shell, motion, state patterns, and accessibility — lives in [design.md](./design.md). This doc owns the **enforced rules** (shadcn-only, i18n, icons, lint); design.md owns the **design system**; the two cross-link rather than duplicate.

## i18n

New user-visible UI copy must be explicitly wired to i18n; do not add hardcoded Chinese.

- New UI copy uses `react-i18next`'s `useTranslation()` / `t("...")`, with keys placed under `frontend/src/i18n/locales/{zh-CN,en}/`; both languages must be filled in at the same time. Copy that lives in the shared package goes to its own bundle and its own hook instead — see [Shared UI Package](#shared-ui-package-agentre-aiagentre-ui).
- The `common` namespace is **one tree physically split into domain modules** — `agents` / `chat` / `common` / `hooks` / `llm` / `org` / `projects` / `remote` / `session` / `settings` (each a `.json`), merged by `locales/<lang>/index.ts`. The module name is **not** part of the key: copy living in `chat.json` is still `t("chatPanel.title")`. Put a new key in the module that owns its top-level prefix, in the same file for both languages. A new module file must be imported into both barrels, and no two modules may claim the same top-level key — `src/__tests__/i18n-locale-modules.test.ts` guards both.
- Do not introduce any bypass text-rewriting mechanism; static UI copy must be wired explicitly to `t(...)` in the component or module.
- Do not translate dynamic content such as agent output, user input, terminal output, file contents, diffs, code blocks, or markdown rendering; by nature it never enters `t(...)`.
- `eslint-plugin-i18next`'s `i18next/no-literal-string` catches hardcoded Chinese copy in JSX text and in visible attributes such as `aria-label` / `title` / `placeholder` / `alt`; if you need to display copy, change it to `t(...)`.
- After changing i18n resources, run:

```bash
cd frontend && pnpm test -- src/__tests__/i18n.test.ts src/__tests__/i18n-locale-modules.test.ts src/__tests__/eslint-i18n.test.ts
cd frontend && pnpm exec eslint src
```

## Shared UI Package (`@agentre-ai/agentre-ui`)

`frontend/packages/agentre-ui` is the frontend layer shared with `agentre-server`. Where it sits and why the dependency only flows one way is [architecture.md](./architecture.md#the-shared-frontend-package-agentre-aiagentre-ui)'s; this section owns the rules for working in it.

Four entry points, so a consumer can take the tokens without the component tree:

| Entry | Contents |
| --- | --- |
| `@agentre-ai/agentre-ui/tokens.css` | design tokens (no JS import needed) |
| `@agentre-ai/agentre-ui/code-highlight.css` | `CodeBlock`'s highlight.js palette |
| `@agentre-ai/agentre-ui/i18n` | locale bundles + namespace, without pulling in components |
| `@agentre-ai/agentre-ui` | transcript renderer, data contract, primitives |

**What belongs in it:** anything both hosts render — transcript components, the row model, the DTO contract, tokens. **What does not:** anything that needs the host's state, navigation, or platform. Reach for a port or a prop instead of importing the host.

**Copy inside the package uses `useUiTranslation()`, never a bare `useTranslation()`.** The package owns a separate namespace (`agentreUi`) so its keys cannot silently collide with the host's `common`; a bare call resolves against the host's default namespace, which *works* while a component is mid-migration and only breaks once the host key is deleted. The host merges the bundle at init — see `src/i18n/index.ts`.

**Depending on a third-party package is a decision, not a detail.** The criterion for `peerDependencies` is one question: *does correctness depend on this being the same instance as the host?* React's hook dispatcher, the host's i18next instance, and anything holding module-level state say yes — a second copy fails at runtime, not at build. Pure rendering and pure functions say no, and a second copy only costs bytes. The current split and the reasoning per package live in the header comment of `packages/agentre-ui/src/boundary.test.ts`, next to the check that enforces it; read that rather than a copy here. `zustand` and `react-router-dom` are deliberately absent from both lists: state and navigation belong to the host.

Three mechanical guards run against the package; when you change what they cover, change them in the same commit:

- `packages/agentre-ui/src/boundary.test.ts` — no host coupling, and every bare import is declared in `package.json`.
- `packages/agentre-ui/src/i18n/i18n.test.tsx` — every static `t("…")` key in the package resolves in **both** bundles (collected via the TS AST, so comments and doc examples don't register as keys).
- `src/components/agentre/__tests__/transcript-dto-contract.test.ts` — the Wails-generated types stay assignable to the package DTO.

```bash
cd frontend/packages/agentre-ui && pnpm test    # the package's own suite
```

The host's vitest run also collects `packages/`, so `cd frontend && pnpm test` covers both.

## Shared Wire Package (`@agentre-ai/agentre-wire`)

`frontend/packages/agentre-wire` publishes the **TypeScript side of the agentre ↔ agentred wire protocol**: the frame types, their codec, and 24 golden samples serialized by the real Go marshaler. Where it sits in the layering is [architecture.md](./architecture.md#the-shared-frontend-package-agentre-aiagentre-ui)'s; this section owns the rules.

It has **zero runtime dependencies** (the codec is pure functions over `JSON.parse` output) and builds with plain `tsc`:

| Entry | Contents |
| --- | --- |
| `@agentre-ai/agentre-wire` | frame types + `decodeXxx` / `encodeXxx` + protocol constants + the `EventKind`, block-type and view-block-type vocabularies |
| `@agentre-ai/agentre-wire/fixtures/<name>.json` | one golden frame, imported directly by a consumer's test |

**`wire.go` is the single source of truth; the TS side is generated from it.** Only three files in `src/` are hand-written, and none of them describes a frame:

| File | Written by | Why |
| --- | --- | --- |
| `src/runtime.ts` | hand | the validation primitives (`decodeWire` / `reqStr` / `optArrOf` / …) the generated code calls. Stable — it does not change when a wire struct does |
| `src/envelope.ts` | hand | the JSON-RPC 2.0 frame shell. Its Go truth (`daemon/rpc.Frame`) sits **outside** the wire package, which deliberately does not depend on `daemon`, so the generator cannot see it — and the RFC froze those six fields anyway |
| `src/index.ts` | hand | re-exports |
| `src/*.gen.ts` | generated | every frame type, codec, protocol constant, and the three vocabularies |
| `fixtures/*.json` | generated | the golden samples |

Three artifacts deliberately step outside "only follow types declared inside the wire package", and **they are not all the same kind of exception** — read the file header before treating one as a precedent for another:

| Artifact | Vocabulary | Why it may step outside |
| --- | --- | --- |
| `src/event-kinds.gen.ts` | `agentruntime.EventKind` | `EventFrame.event` is a `json.RawMessage` — the payload is opaque to wire and can only ever generate as `unknown`, so the `kind` discriminator is the single typed thing in the whole event stream, and it is the contract itself. `agentruntime` is a direct dependency of wire (`wire.go` already uses its `TurnKind` / `MCPServerSpec`), so following it breaks no layering |
| `src/block-types.gen.ts` | `blocks.StoredBlock.type` | same argument one link down: `StoredBlock.data` is a `json.RawMessage`, so `type` is the only typed thing on the `HistoryMessageWire.blocks` / `RunParams.userBlocks` paths, and the block registry package is already wire's direct dependency |
| `src/chat-block-types.gen.ts` | `chat_svc.ChatBlock.type` | **a different kind of exception — this one is not on the wire at all.** It is the view DTO for the backend → frontend hop (Wails binding on the desktop, HTTP for the web console). It lives here only because this package is the repo's single Go → TS generation seam, and the vocabulary had two frontends hand-copying it |

**Neither is a precedent for generating everything a neighbouring package declares.** Each artifact restates its own reasoning in its header, and next to its declaration list in the generator.

`block-types.gen.ts` and `chat-block-types.gen.ts` are **two different tables and both are exported** — the projection in `chat_svc` renames (`user_ask` → `ask_user_question`), folds many-to-one (`nested_tool_use` → `tool_use`) and drops whole classes (`subagent_state`) between them. The cells that share a name (`text` / `thinking` / `plan` / …) are places the projection happened not to rename, not one truth. The view vocabulary's truth boundary is drawn at **what Go can emit**: the frontend's `TranscriptBlock.type` currently holds one more value, `"raw"`, produced by `peer-transcript.ts`, and its truth stays on the TS side.

**Do not hand-edit a `*.gen.ts` file or a fixture, and do not add a runtime dependency to this package.** Both generators live next to the Go types they mirror and own their own file lists:

```bash
# frame types + codec + constants → src/*.gen.ts
WIRE_TS_WRITE=1 go test ./internal/pkg/agentruntime/runtimes/remote/wire/ -run TestWriteTSCodec
# golden samples → fixtures/*.json
WIRE_GOLDEN_WRITE=1 go test ./internal/pkg/agentruntime/runtimes/remote/wire/ -run TestWriteGoldenSamples
```

Writing is deliberately an explicit action, so a plain `make test-backend` never dirties the working tree. **The generator emits Prettier-formatted output itself** — formatting has to be part of the artifact, because the freshness guard compares regenerated bytes against committed bytes and an external formatting pass would make it permanently red.

**Staleness and coverage are both caught mechanically**, by four always-on guards in the wire package (see [testing.md](./testing.md#guard-tests)): the committed `*.gen.ts` and `fixtures/*.json` must be exactly what the generators write today, and the generator's lists must cover everything the wire package exports **plus** every `EventKind` `agentruntime` declares. Adding a Go field, a whole Go struct, or a new event kind turns the build red instead of silently leaving the browser decoding an obsolete shape. The guards are self-contained within this repository and never read a sibling checkout.

The package's own suite (`src/__tests__/wire-codec.test.ts`) decodes every golden sample and asserts the result field by field, plus unknown-field preservation and the legacy (omitted-optional) shapes — so the codec's behavior is verified next to the Go truth it is generated from, not one repository away.

## Project Structure

- `frontend/components.json` defines the aliases: `@/components`, `@/lib/utils`, `@/components/ui`, `@/lib`, `@/hooks`.
- Routing uses `MemoryRouter`.
- Stores live in `frontend/src/stores`, hooks in `frontend/src/hooks`.
- Wails runtime / bindings are imported from `frontend/wailsjs`.
- Keep the existing dense desktop-app layout for the UI; **do not** write landing-page styling into the app shell.
- For icons used in user operations, prefer the `lucide-react` and Iconify Tabler already in use in the project; **do not** hand-draw inline SVG.

## Package Management

`pnpm` is the source of truth; **do not** use npm.

```bash
cd frontend && pnpm install              # install dependencies
cd frontend && pnpm add <pkg>            # add a package
cd frontend && pnpm remove <pkg>         # remove a package
cd frontend && pnpm test                 # vitest (happy-dom)
cd frontend && pnpm test -- path/to/file.test.tsx   # single file
```

`make test-frontend` runs `make generate` first and then `pnpm test`; use it when the wails bindings need to be regenerated. Vitest is configured with happy-dom and aliases the wails imports to a mock, so most tests can run even without a `frontend/wailsjs/` directory.

## Formatting / Lint

Go:

```bash
gofmt -w <files>
goimports -w <files>       # local-prefixes: github.com/agentre-ai/agentre
make lint                  # golangci-lint + frontend ESLint
make lint-fix              # auto-fix (use on a small scope)
```

`goimports` groups local imports under the `github.com/agentre-ai/agentre` prefix, matching `.golangci.yml`.

Frontend: follow the existing TS/CSS style; **do not** introduce a large formatting-only diff.

## Module Path

The Go module is `github.com/agentre-ai/agentre`; use that prefix for in-repo Go imports.
