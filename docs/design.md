# Agentre Design System

> **A reuse-oriented design reference.** It consolidates the visual language that lives in [`frontend/src/styles/globals.css`](../frontend/src/styles/globals.css) and the shadcn + `components/agentre` layers into one place you can copy from: **color tokens (full light/dark values), the theming mechanism, the component palette, the desktop window shell, motion, state patterns, accessibility, and an end-to-end new-page recipe.** Read this before building any new page, dialog, or block so it stays visually and behaviorally consistent with the rest of the app.

> **Stack in one line:** React 19 + shadcn/ui (Radix primitives, `new-york` style) + Tailwind CSS v4 + React Router (MemoryRouter) inside a **Wails desktop window**. Colors, fonts, and motion are defined in the `@theme inline` block of [`frontend/src/styles/globals.css`](../frontend/src/styles/globals.css). **There is no `tailwind.config.js`** (Tailwind v4, wired via `@tailwindcss/vite`); **class names have no prefix** (`bg-background`, not `tw-bg-background`); `cn()` lives at [`frontend/src/lib/utils.ts`](../frontend/src/lib/utils.ts); `baseColor` is `neutral` ([`components.json`](../frontend/components.json)).

---

## 0. What this doc owns

| Owned here | Owned elsewhere |
| --- | --- |
| Color-token values, semantics, usage; the agent palette & status system | The enforced rules (shadcn `@/components/ui/*`-only, i18n, `lucide`/Iconify icons, lint, no hardcoded Chinese) → [`frontend.md`](./frontend.md) |
| Theming mechanism, `dark:` usage, the desktop window shell | Layering / dependency direction, `internal/app` ↔ service ↔ repository, storage paths → [`architecture.md`](./architecture.md) |
| Component palette, variants, selection guidance | TDD / SOLID / commit style → [`develop.md`](./develop.md); test design → [`testing.md`](./testing.md) |
| Elevation (surfaces & shadows), z-index, motion, state patterns, accessibility, page recipe | Doc fact-checking discipline → [`documentation.md`](./documentation.md) |

This doc restates the [`frontend.md`](./frontend.md) hard rules only where needed, then links back — it does not duplicate them.

---

## 1. Core Constraints (non-negotiable)

Every UI change must satisfy all of these. They are the bar for "consistent, friendly UI/UX" in this codebase.

- **Use tokens, not literal colors — one value, one place.** Never write a hex (`#3b6896`), an `rgb()`, or a palette class (`text-blue-500`). Always use a semantic token — `bg-background`, `text-foreground`, `border-border`, `text-primary`, `text-primary-text`, `text-muted-foreground`, … (§3). All color values live in exactly one place — the token definitions in [`globals.css`](../frontend/src/styles/globals.css) — so the palette stays unified and a single edit re-skins everything. One semantic concept maps to **one** token: before adding a color, check §3 for an existing token and reuse it; don't introduce a near-duplicate. Only add a new token when the concept is genuinely new — with both light and dark values — and document it in §3.

  > **Sanctioned literal-color exceptions** (everything else must be a token): the xterm ANSI
  > palette in [`terminal/terminal-theme.ts`](../frontend/packages/agentre-ui/src/terminal/terminal-theme.ts)
  > (xterm.js can't consume CSS variables); the `#94a3b8` slate **avatar fallback** when agent meta is
  > missing (§3.6); neutral black-alpha **shadows/scrim** (`box-shadow rgba(0,0,0,…)`, the `Dialog`
  > backdrop) — there are no `--shadow-*` tokens by design (§3.12); and `bg-neutral-600` as the
  > **`"neutral"` agent** identity fill (§3.6).
- **Both themes, always.** Light and dark are first-class. Because every color comes from a token that has a `:root` and a `.dark` value, using tokens makes a component theme-correct for free. Verify on real light *and* dark before considering anything done (§4).
- **This is a desktop window, not a web page.** The shell is a fixed Wails frame — title bar → icon rail → resizable sidebar → tab strip → panel → status bar (§7). `html, body, #root` are `height:100%; overflow:hidden`, and the body defaults to `user-select:none`; text selection is **opted into** per region, not the default (§7). There is **no mobile breakpoint** and no `useIsMobile` — design for a resizable desktop window (min `860×640`), not a phone.
- **No inline `style={{}}` for what Tailwind can express.** Compose utility classes via `cn()` (`clsx` + `tailwind-merge`); build variants with `class-variance-authority` (CVA). Inline styles only for genuinely dynamic values (a computed width, a per-agent `var(--agent-N)` color, a `display` toggle).
- **Hover/focus are CSS, not state.** Express interactive visuals with pseudo-classes (`hover:bg-accent`, `focus-visible:ring-ring/50`). React state is for data/logic, not styling.
- **Reuse components before building new ones.** Default to the shadcn primitives in [`frontend/src/components/ui/`](../frontend/src/components/ui/) (§6) and the project blocks in [`frontend/src/components/agentre/`](../frontend/src/components/agentre/); icons come from `lucide-react` (with `@iconify/react` for the agent icon registry) — don't hand-roll a control that already exists. When the same block appears in two or more places, extract one shared component instead of copy-pasting.
- **Keep motion restrained, and honor reduced motion.** Enter/leave in `150–200ms`, `ease-out`; reuse `tw-animate-css` utilities and Radix `data-state` rather than inlining keyframes; prefer `transition-colors`/`transition-transform` over `transition-all`. Every animation carries a `motion-reduce:` (or `motion-safe:`) modifier (§8).
- **No silent operations.** Every async flow surfaces loading / empty / error / success. The user must always know whether their action worked (§9).
- **All visible UI copy goes through i18n.** New text uses `react-i18next`'s `t(...)` and updates both `frontend/src/i18n/locales/zh-CN/` and `.../en/` (domain modules merged by `index.ts`). Do not hardcode Chinese (ESLint blocks it). Do **not** translate dynamic agent / user / terminal / markdown output. This is a [`frontend.md`](./frontend.md) hard rule — see it for details.
- **Don't introduce new colors or fonts ad hoc.** New color → add a token in [`globals.css`](../frontend/src/styles/globals.css) (with both light and dark values) and document it here. New font → add a `--font-*` token; don't reference an unconfigured family.

---

## 2. Design Principles

The "why" behind the constraints — apply these when shaping a screen.

1. **Agent identity is visual.** Agentre coordinates many concurrent coding agents, so an agent must be recognizable at a glance. Identity is carried by **a 16-color agent palette** (`--agent-1…16`, §3.6) seeded from the agent's color token, and run state by **four status tokens** (`running` / `waiting` / `idle` / `error`, §3.5) rendered as dots and pills. Reuse `AgentAvatar` / `StatusDot` / `StatusPill` — don't re-derive agent color or re-style status.
2. **System state is always visible.** No silent work. Each async flow shows loading → progress → result (toast or in-place state, §9). A turn that finishes, errors, or needs approval also raises an app notification (§6.5).
3. **Color is semantic, never decorative.** The steel-blue `primary` = interactive / brand; `status-running` green = running / success; `status-waiting` amber = needs attention; `status-error` / `destructive` red = error / dangerous. The agent hues are **identity**, not status — never read meaning into *which* agent color a session has.
4. **One desktop frame.** Every screen lives in the same shell — custom title bar, a `w-14` icon rail, an optional resizable sidebar, the chat tab strip, and a status bar (§7). Swap the content in the `Outlet`/panel, not the frame.
5. **Depth is a surface step, not a shadow.** Surfaces form an elevation ladder (light: `bg` → `card`/`popover`; dark: `rail` → `sidebar` → `bg` → `card` → `popover`). Separate layers with the surface token + a `border`; shadows are a faint accent at most (§3.12). This matters because shadows barely render on dark surfaces.
6. **High cohesion, low coupling.** Each UI unit has a single purpose, a clear interface, and is understandable and testable on its own. A file growing large is usually a signal to split it.

---

## 3. Color Tokens (full light / dark values)

**Single source:** [`frontend/src/styles/globals.css`](../frontend/src/styles/globals.css). `:root` defines light, `.dark` overrides for dark, and the `@theme inline` block exposes every `--token` as a Tailwind color (`--color-*`), so `bg-<token>` / `text-<token>` / `border-<token>` all work **and switch with the theme automatically**.

**Usage:**
- Background `bg-<token>`, text `text-<token>`, border `border-<token>`, focus ring `ring-ring`.
- Opacity modifiers compose directly: `bg-primary/90`, `ring-destructive/20`, `border-status-error/40`.
- **Never hard-code a color value** — see Constraint 1. For a dark-only tweak use the `dark:` variant.

### 3.1 Base surfaces & text hierarchy

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `background` | `#fafafa` | `#17191c` | Page / content background |
| `foreground` | `#18181b` | `#e6e8eb` | Primary text |
| `card` | `#ffffff` | `#1d2025` | Card / raised surface |
| `card-foreground` | `#18181b` | `#e6e8eb` | Text on cards |
| `popover` | `#ffffff` | `#262931` | Floating layers (dropdown / tooltip / toast) surface |
| `popover-foreground` | `#18181b` | `#e6e8eb` | Text in floating layers |
| `rail` | `#e4e4e7` | `#0a0b0d` | Window chrome bands — title bar, icon rail, status bar (the recessed frame) |
| `muted-foreground` | `#65656d` | `#909399` | De-emphasized / descriptive text — timestamps, counts, metadata labels, section headings. **This is the floor for anything a user has to read.** Its value is set by the *darkest* surface it lands on, not by `card`: the status bar and window controls put it on `rail`, where the old `#71717a` was only 3.81. Now 4.55 on `rail`, 5.26 on `secondary`/`code-surface`, 5.78 on `card`. Guarded per-surface by [`tokens.test.ts`](../frontend/packages/agentre-ui/src/tokens.test.ts) |
| `decorative-foreground` | `#a1a1aa` | `#5a5d64` | **Glyphs that never carry information** — separator dots (`·` `/` `›`), diff/file line numbers, `aria-hidden` icons that merely accompany adjacent text, fallback glyphs. At ~2.5:1 it misses 3:1 in both themes **by design**. Was named `subtle-foreground`; that name read like "a weaker body text", so 97 metadata labels quietly ended up on it (2026-08-19 audit) — they all moved to `muted-foreground`. If the thing has to be *read*, it does not belong here |

> Dark mode is a deliberate **5-level surface ladder**: `rail #0a0b0d` < `sidebar #111316` < `background #17191c` < `card #1d2025` < `popover #262931`. Pick the surface that matches the layer's height (§3.12).

### 3.2 Brand primary (steel-blue)

A muted, cool steel-blue chosen to stay distinct from the bright agent blues (§3.6).

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `primary` | `#3b6896` | `#5b8dbf` | **Solid** brand fill (the `Button`/`Badge` `default` variant is `bg-primary text-primary-foreground`) and accent borders/indicators |
| `primary-foreground` | `#ffffff` | `#0a1420` | Text/icons on solid `primary` |
| `primary-soft` | `#eef4fa` | `#1a2738` | Soft brand wash — active sidebar items, icon backgrounds, info toasts |
| `primary-text` | `#3b6896` | `#8eb6dc` | Brand-tinted **text/icon** on a soft/neutral background (dark is brightened for contrast vs. solid `primary`) |
| `ring` | `#3b6896` | `#5b8dbf` | Focus ring (`focus-visible:ring-ring/50`) |

> Note the split: `primary` is the solid fill, `primary-text` is the readable on-light tint. Use `text-primary-text` for brand-colored text on `primary-soft`/`card`; use `bg-primary text-primary-foreground` for a solid control.

### 3.3 Secondary / muted / accent

> Per the shadcn convention these share the **same gray value** in each theme — different semantics, one fill.

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `secondary` | `#f4f4f5` | `#262931` | Secondary buttons / fills; the tab-strip band |
| `secondary-foreground` | `#3f3f46` | `#c4c7cd` | Text on secondary |
| `muted` | `#f4f4f5` | `#1d2025` | Muted background (group fills, placeholders) |
| `accent` | `#e0e0e3` | `#383d47` | **交互反馈** —— 内容表面上的 hover / 选中填充。刻意不等于任何静止表面：曾经是 `#f4f4f5`，与 `secondary`/`muted`/`sidebar` 同字节，86 处 `hover:bg-accent` 在那些面上渲染成 1.00:1。实测 card/popover 1.32、background 1.26、secondary 1.20。**外壳带上的 hover 用 `rail-accent`，不要用它** |
| `rail-accent` | `#f7f7f8` | `#212429` | 窗口外壳带（标题栏 / 图标栏 / 状态栏，即所有 `bg-rail` 之上）的 hover / focus 反馈。`rail` 亮色是 `#e4e4e7`，比任何内容表面都暗得多，**一个值无法同时服务两边**：在白卡片上够深的填充落到 rail 上会被吃掉（2026-08-19 就这么把 rail 的 hover 压到过 1.028）。rail 是下沉的一层，所以 hover 是提亮而非压暗，与 `sidebar-active-bg` 一致。实测 rail 上 1.19 / 1.27 |
| `accent-foreground` | `#18181b` | `#e6e8eb` | Text on accent |

### 3.4 Borders, inputs, ring

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `border` | `#e4e4e7` | `#2a2d34` | Global borders (the `@layer base` reset gives every element `border-border`) |
| `border-strong` | `#d4d4d8` | `#3a3e47` | Emphasized dividers / drag handles where `border` is too faint |
| `input` | `#cbcbd0` | `#4a4f59` | Field edges (`Input` / `Textarea` / `Select` / outline `Button`). Split off from `border` so a divider can stay quiet while a field edge stays legible. Target is "clearly visible", **not** WCAG's 3:1 — these controls carry their own fill and text, so the border is a supporting cue; controls whose border *is* the control use `control-border` below |
| `input-bg` | `#ffffff` | `#17191c` | Form-control fill |
| `control-border` | `#8a8a91` | `#70757f` | **Controls whose outline *is* the control** — an unchecked `Checkbox` has no fill, so losing the border loses the control. Sized to clear WCAG 3:1 on the worst surface a control lands on (`secondary`: 3.12 light / 3.14 dark). Do **not** reach for `border`/`input` here: they are quiet dividers/field edges at ~1.1:1 against every surface, which is why the model table's header select-all used to vanish. Guarded by [`ui/__tests__/checkbox.test.tsx`](../frontend/src/components/ui/__tests__/checkbox.test.tsx) |

### 3.5 Status colors (agent run state)

The heart of agentre's state language. Four states, each with a solid color (dots/text) and — for `running`/`waiting`/`error` — a soft tinted background (pills). Driven by `statusConfig` in [`components/agentre/types.ts`](../frontend/src/components/agentre/types.ts).

| State | solid (`status-*`) Light → Dark | bg (`status-*-bg`) Light → Dark | Rendered as |
| --- | --- | --- | --- |
| `running` | `#10b981` → `#34d399` | `#ecfdf5` → `#0f2218` | green dot / pill — agent is working |
| `waiting` | `#f59e0b` → `#fbbf24` | `#fffbeb` → `#261d0d` | amber dot / pill — awaiting approval/input |
| `idle` | `#a1a1aa` → `#6a6d74` | *(uses `secondary`)* | gray dot; text falls to `muted-foreground` |
| `error` | `#dc2626` → `#f87171` | *(uses `destructive-soft`)* | red dot / pill — turn failed |

**Each state has up to four roles — do not mix them up:**

| Role | Token | Renders as |
| --- | --- | --- |
| fill | `status-<state>` | dots, solid badges, progress |
| soft fill | `status-<state>-bg` | the pill background |
| on-fill text | `status-<state>-foreground` | text sitting **on** the saturated fill |
| as text | `status-<state>-text` | the state rendered **as text** (on `status-*-bg` or a card) |

| Token | Light | Dark | Why it exists |
| --- | --- | --- | --- |
| `status-running-foreground` | `#ffffff` | `#04140c` | Text on the solid green |
| `status-waiting-foreground` | `#402b06` | *(same)* | Deep brown on the bright amber fill. Both themes keep a bright amber, so one value reads on either |
| `status-running-text` | `#047857` | `#34d399` | The saturated fill is unreadable **as text** in light: `#10b981` on its own pill is 2.41. Dark already cleared the bar, so it reuses the fill value |
| `status-waiting-text` | `#b45309` | `#fbbf24` | Same story: `#f59e0b` on its own pill is 2.07 |

The `-text` split is guarded by [`packages/agentre-ui/src/tokens.test.ts`](../frontend/packages/agentre-ui/src/tokens.test.ts) (≥4.5 on both the pill and `card`, both themes). Render status only through `StatusDot` / `StatusPill` (§6.4) so the dot/pill/label stay in lockstep; labels are uppercase (`RUNNING`).

### 3.6 Agent palette (16 identity colors)

Sixteen fixed hues give concurrent agents distinct, stable identities. Light uses saturated **600–700** shades that hold up on light surfaces; dark uses lighter **300–400** shades. `agent-3` (sky) and `agent-6` (cyan) are tuned to avoid clashing with `status-running` green and the steel `primary`.

| Token | Light | Dark | | Token | Light | Dark |
| --- | --- | --- | --- | --- | --- | --- |
| `agent-1` | `#2563eb` | `#60a5fa` | | `agent-9` | `#4f46e5` | `#818cf8` |
| `agent-2` | `#7c3aed` | `#a78bfa` | | `agent-10` | `#ea580c` | `#fdba74` |
| `agent-3` | `#0284c7` | `#38bdf8` | | `agent-11` | `#059669` | `#34d399` |
| `agent-4` | `#e11d48` | `#fb7185` | | `agent-12` | `#0d9488` | `#2dd4bf` |
| `agent-5` | `#d97706` | `#fbbf24` | | `agent-13` | `#db2777` | `#f472b6` |
| `agent-6` | `#0891b2` | `#22d3ee` | | `agent-14` | `#ca8a04` | `#fde047` |
| `agent-7` | `#c026d3` | `#e879f9` | | `agent-15` | `#64748b` | `#94a3b8` |
| `agent-8` | `#65a30d` | `#a3e635` | | `agent-16` | `#9333ea` | `#c084fc` |

> The initial glyph sitting **on** an agent fill uses `agent-foreground` (`#ffffff`, theme-invariant — the letter is white on all sixteen hues in both themes). Use `text-agent-foreground`, not a literal `text-white`.

**How to apply a color.** The source of truth is the agent's `agentColor` token (e.g. `"agent-7"`), assigned by the backend — there is **no client-side hashing**. Map it through the helpers, never by hand:

- Tailwind classes (the common path) — [`components/agentre/types.ts`](../frontend/src/components/agentre/types.ts): `agentColorClassNames[color]` → `bg-agent-7`; `agentTextColorClassNames[color]` / `agentTextColorClassName(token, fallback)` → `text-agent-7`. The extra member `"neutral"` maps to `bg-neutral-600` / `text-foreground`.
- A raw CSS color (for inline `style`) — [`components/agentre/session-avatar.ts`](../frontend/src/components/agentre/session-avatar.ts): `tokenToCssColor(token)` → `var(--agent-7)` or `null` for an invalid token; `avatarFromMeta(meta)` → `{ letter, color }`, falling back to `#94a3b8` (slate) when meta is missing.

Default fallback color is `agent-1`. `agentColorOrder` is the canonical 1→16 sequence for round-robin assignment.

### 3.6a File identity (8 semantic hues)

The file-type icon uses a transparent 17px alignment slot containing a directly colored 16–17px Tabler Brand Logo or file-type glyph. Eight semantic identity hues (`--file-*`) are assigned by the unified path classifier in [`components/agentre/file-type-icon.tsx`](../frontend/src/components/agentre/file-type-icon.tsx). Like the agent palette, light uses saturated 500–600 shades and dark uses lighter 400 shades. The glyph shape always carries a second, non-color cue; these tokens only aid scanning.

| Token / class | Light | Dark | Typical identities |
| --- | --- | --- | --- |
| `file-blue` | `#2563eb` | `#60a5fa` | TypeScript, React TS, C/C++, CSS, SQL, Markdown, config, Docker, Word |
| `file-yellow` | `#ca8a04` | `#facc15` | Python, JavaScript, React JS, JSON, key / cert |
| `file-cyan` | `#0891b2` | `#22d3ee` | Go |
| `file-purple` | `#7c3aed` | `#a78bfa` | Kotlin, C#, Sass, PHP, image, audio |
| `file-orange` | `#ea580c` | `#fb923c` | Rust, Java, HTML, Swift, XML, Git, PowerPoint, SVG, archive |
| `file-green` | `#16a34a` | `#4ade80` | shell, Makefile/CMake, Excel, CSV, font, database |
| `file-red` | `#dc2626` | `#f87171` | YAML, Ruby, npm, PDF, video, binary |
| `file-neutral` | `#71717a` | `#8a8d94` | plain text / log, TOML, `*.lock`, unknown fallback |

Use `text-file-<tone>` (exposed via `--color-file-*` in the `@theme inline` block); never write the hex directly. The slot has no background, border, radius, shadow or padding, and selected/hover backgrounds belong to the containing row or tab. High-recognition languages use the installed Tabler Brand Logo where available; formats use their file-type glyph. Directory rows remain separate and keep neutral `Folder` / `FolderOpen` plus Chevron icons. The icon itself is decorative (`aria-hidden`) — file names, Git status and actions keep carrying the semantics.

### 3.6b Issue label tones (2 extra hues)

The ten issue-label chips in [`components/agentre/issue-tones.ts`](../frontend/src/components/agentre/issue-tones.ts) are all "soft fill + a text color readable on that fill". Eight of them borrow an existing semantic family — `destructive-soft`/`destructive-text` (bug), `destructive` (critical), `secondary`/`secondary-foreground` (docs, ops), `status-running-*` (feature), `status-waiting-*` (perf), `primary-soft`/`primary-text` (hook, refactor). Two hues have no semantic home; they exist only to keep ten labels apart, so they get their own pair here.

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `tone-blue-bg` | `#e9effd` | `#242d3a` | Soft blue chip fill (`auth`) |
| `tone-blue-text` | `#1d4ed8` | `#60a5fa` | Text on `tone-blue-bg` |
| `tone-violet-bg` | `#f2ebfd` | `#2b2b3a` | Soft violet chip fill (`ui`) |
| `tone-violet-text` | `#6d28d9` | `#a78bfa` | Text on `tone-violet-bg` |

**Do not reach into the agent palette for this.** These two used to be `bg-agent-1/10 text-agent-1` / `bg-agent-2/10 text-agent-2`, which broke twice over: the `--agent-*` hues are *identity* built for `bg-agent-N` + a white glyph (§3.6) — as text on a card, half the sixteen miss 4.5 — and a `/10` tint is transparent, so the chip's real fill (and its contrast) shifted with whatever surface it landed on: `auth` measured 4.49 on `card`, 4.33 on `background`, 4.06 on a hovered list row. The fills above are the opaque equivalent of what the old tint rendered on a card, so the chips look the same but no longer depend on what is underneath. Guarded by [`components/agentre/__tests__/issue-tones.test.ts`](../frontend/src/components/agentre/__tests__/issue-tones.test.ts), which reads the classes back out of `issue-tones.ts`, resolves them through `tokens.css`, and asserts ≥ 4.5 for every tone on every surface, both themes.

### 3.7 Sidebar

A dedicated family so the navigation rail and context sidebars theme independently of the page surfaces.

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `sidebar` | `#f4f4f5` | `#111316` | Context-sidebar background (chat/projects lists) |
| `sidebar-foreground` | `#18181b` | `#e6e8eb` | Sidebar text |
| `sidebar-accent` | `#f4f4f5` | `#262931` | Hover/selected background |
| `sidebar-active-bg` | `#ffffff` | `#262931` | Active item fill |
| `sidebar-border` | `#e4e4e7` | `#2a2d34` | Sidebar border |
| `sidebar-icon` | `#71717a` | `#8a8d94` | Rail icon (resting) |
| `sidebar-icon-active` | `#3b6896` | `#5b8dbf` | Rail icon (active) |

(Also `sidebar-primary` / `-primary-foreground` / `-accent-foreground` / `-ring`, equal to the corresponding primary / text values.)

### 3.8 macOS traffic lights

| Token / class | Value (both themes) | Use |
| --- | --- | --- |
| `traffic-close` | `#ff5f57` | Close-button red |
| `traffic-minimize` | `#febc2e` | Minimize amber |
| `traffic-zoom` | `#28c840` | Zoom green |

> On macOS the **OS draws the native traffic lights** in a 68px inset reserved by the title bar — these tokens exist for parity/reference; the custom Windows controls are rendered by `WindowsWindowControls` (§7). Don't hand-paint traffic lights on macOS.

### 3.9 Charts

`chart-1…5` are defined in `oklch` for data viz (distinct light/dark values). Use `bg-chart-N` / `text-chart-N`; reserve them for charts, not general UI.

### 3.10 Scrollbar

The scrollbar auto-hides via a CSS variable rather than a class (a WKWebView repaint quirk — see §7).

| Token | Light | Dark |
| --- | --- | --- |
| `--sb-thumb` | `transparent` (visible on scroll) | `transparent` (visible on scroll) |
| `--sb-thumb-strong` | `color-mix(… muted-foreground 45%)` | same formula |

Don't restyle scrollbars per-container; the global rules in [`globals.css`](../frontend/src/styles/globals.css) cover both the Firefox `scrollbar-*` properties and the WebKit pseudo-elements. To hide one entirely (e.g. a horizontal strip), add `.scrollbar-none`.

### 3.11 Destructive

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `destructive` | `#dc2626` | `#f87171` | Dangerous / delete / error actions |
| `destructive-foreground` | `#ffffff` | `#fafafa` | Text on solid destructive |
| `destructive-soft` | `#fef2f2` | `#2a1414` | Soft red wash — error cards, error toasts, the `error` status pill |
| `destructive-text` | `#b91c1c` | `#f87171` | **Red rendered as text** on `destructive-soft` / a card. The same fill-vs-text split as `status-*-text`: `destructive` on its own wash is only 4.41 in light. Keep `destructive` for fills, dots and icon marks; reach for this one whenever the red *is* the text. Dark already clears the bar on the wash (6.28), so it reuses the fill value |

### 3.11a Code / console surface

Monospace **console output** surfaces (hook stdout/stderr, local-command output) — theme-adaptive, distinct from the `secondary`-based `CodeBlock` container.

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `code-surface` | `#f4f4f5` | `#121418` | Console/output box fill (`bg-code-surface`) |
| `code-foreground` | `#3f3f46` | `#e6e8eb` | Primary monospace text on `code-surface` |
| `code-muted-foreground` | `#65656d` | `#9aa0ab` | De-emphasized monospace text (stdout) |

### 3.12 Elevation (surfaces & shadows)

Depth is primarily a **surface step**, not a shadow (Principle 5). Pick the surface that matches the layer:

| Level | Surface token | Shadow | Use |
| --- | --- | --- | --- |
| **Recessed chrome** | `rail` | none | Title bar, icon rail, status bar |
| **Base** | `background` | none | Page content |
| **Resting card** | `card` | none / `shadow-xs` | Cards, list rows, the active sidebar item (`shadow-xs`). Prefer a `border` over a shadow at rest. |
| **Raised** | `popover` | `shadow-md` | Anchored floating layers — `DropdownMenu`, `Popover`, `HoverCard`, `Select`, the rail tooltip |
| **Overlay** | `card` | `shadow-overlay` | Detached overlays that own the screen — `Dialog`. The shadow comes from the `--overlay-shadow` token (light: a soft drop + dark hairline; dark: a heavier drop + **light** hairline), and the backdrop from `--overlay-scrim` (`bg-scrim`) |

- **Shadows barely render in dark.** On the dark surfaces a black shadow is nearly invisible, so depth in dark relies on the surface step + the `border`. Keep the border; don't reach past `shadow-overlay`.
- **The one shadow token is `--overlay-shadow` → `shadow-overlay`.** Root token is `--overlay-*` while the utility alias is `--shadow-*` on purpose: Tailwind v4's shadow namespace *is* `--shadow-*`, so a same-named root token would make the `@theme` mapping self-referential. Same reason for `--overlay-scrim` → `--color-scrim`. Anything shallower (`shadow-xs` / `shadow-md`) stays a plain Tailwind utility — don't invent more shadow tokens.
- Pair elevation with the matching radius (§5): raised → `rounded-lg`, overlay → `rounded-xl`.

---

## 4. Theming

**Mechanism:** the theme switches by adding/removing `.dark` on `document.documentElement` (`@custom-variant dark (&:is(.dark *))` is what makes the `dark:` variant work). The toggle is `applyDocumentTheme(theme)` in [`frontend/src/App.tsx`](../frontend/src/App.tsx), which also sets `data-theme` and `style.colorScheme` for redundancy. Every token is defined under both `:root` and `.dark`, so toggling the class re-skins the whole app — no per-component color changes needed.

**Preference & API.** There is **no `ThemeProvider`/`useTheme`**; theme is React state in `AppLayout`, passed down via the router `Outlet` context. The model (types in [`components/agentre/chrome.tsx`](../frontend/src/components/agentre/chrome.tsx)):

```ts
type AppTheme = "light" | "dark";
type AppThemePreference = AppTheme | "system";   // user choice
// effectiveTheme = resolveThemePreference(preference, systemTheme)
```

- **Persistence:** `localStorage` key `"agentre.theme"` (client-side only; no backend/Wails binding).
- **System sync:** a `matchMedia("(prefers-color-scheme: dark)")` listener updates `systemTheme`; when preference is `"system"`, `effectiveTheme` follows the OS live.
- **Toast theme:** `<Toaster theme={effectiveTheme} />` keeps Sonner in sync (§6.5).

**Known gap — no flash prevention.** `.dark` is applied in a `useLayoutEffect`, *after* first paint, and there is no inline pre-mount script in `index.html`/`main.tsx`. A dark-mode user can see a brief light frame on cold start. If you add flash-prevention, set `.dark` before React mounts and document it here.

**Correct usage (do / don't):**

```tsx
// ✅ Tokens — adapt to light/dark automatically
<div className="bg-card text-foreground border-border">…</div>
<button className="bg-primary text-primary-foreground hover:bg-primary/90">…</button>

// ✅ dark: variant only for a dark-specific tweak
<div className="bg-input/30 dark:bg-input/50">…</div>

// ❌ Hard-coded colors — break in dark and violate Constraint 1
<div className="bg-white text-[#18181b] border-[#e4e4e7]">…</div>
```

**Every UI change must hold up in both themes.** Verify on real light and dark — don't ship after checking only one.

---

## 5. Typography, Radius & Spacing

### Fonts

**System-font-only, zero webfonts** — declared as two tokens in the `@theme inline` block of [`globals.css`](../frontend/src/styles/globals.css). A desktop app must work offline and pays for every byte, so the type system is the platform's own fonts. Both stacks pin an **explicit CJK fallback** (`PingFang SC` / `Microsoft YaHei` / `Noto Sans SC`) because agentre is Chinese-first, and the sans stack pins emoji families so status badges don't drop emoji.

| Token | Use |
| --- | --- |
| `font-sans` (`--font-sans`) | Body / UI text. Applied on `body` via `@apply font-sans`, so everything inherits it; you rarely write `font-sans` explicitly. Stack: `-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans SC", Roboto, … sans-serif, "Apple Color Emoji", "Segoe UI Emoji", "Segoe UI Symbol"`. |
| `font-mono` (`--font-mono`) | Code, model names, IDs, status pills, paths — anything monospaced (`font-mono`). Stack: `ui-monospace, "SF Mono", Menlo, Monaco, "Cascadia Code", Consolas, … "PingFang SC", "Microsoft YaHei", monospace`. |

> **No `@font-face`, no CDN.** Don't reference a family that isn't actually packaged (it would silently fall back and mislead). Code-block syntax highlighting is styled in [`code-highlight.css`](../frontend/packages/agentre-ui/styles/code-highlight.css).

### Type scale

Standard Tailwind steps, plus one custom step below `text-xs`:

| class | Size | Use |
| --- | --- | --- |
| `text-2xs` | `0.6875rem` (11px) | Dense metadata — status pills, timestamps, chips, line numbers, avatar initials (`--text-2xs`, picked up by Tailwind's `--text-*` convention) |
| `text-xs` | 12px | Secondary body, descriptions, most list copy |
| `text-sm` | 14px | Default UI text, titles, buttons |

### Radius

`--radius: 0.5rem` (8px) is the base; four steps are derived via `calc` in `@theme inline`:

| class | Value | Typical use |
| --- | --- | --- |
| `rounded-sm` | 4px | Status pills, compact tags |
| `rounded-md` | 6px | Buttons, inputs, selects (badges are `rounded-full`) |
| `rounded-lg` | 8px | Cards, panels, rail buttons, avatars (md/lg) |
| `rounded-xl` | 12px | Dialogs, large emphasized containers |

### Chrome dimensions & spacing rhythm

The desktop frame uses fixed band heights — match them when extending the shell:

- **Title bar** `h-11` (44px) · **Icon rail** `w-14` (56px) · **Tab strip** `h-[38px]` · **Status bar** `h-7` (28px).
- **Context sidebars** (chat/projects): default `320px`, min `220px`, max `640px`, drag-resized and persisted (§7).
- **Window:** min `860×640`, fully resizable.
- **Block spacing:** start sections at `gap-2`/`gap-3`; card padding `p-4`–`p-6`; rail padding `px-2 py-3`.

---

## 6. Component palette & usage

The shadcn primitives live in [`frontend/src/components/ui/`](../frontend/src/components/ui/) — `new-york` style, CSS variables enabled, no class prefix ([`components.json`](../frontend/components.json)). Icons are `lucide-react` (plus `@iconify/react` for the agent icon registry); class merging is `cn()` ([`frontend/src/lib/utils.ts`](../frontend/src/lib/utils.ts)); variants are CVA — the conventions you'll see in every primitive's source. The **enforced** shadcn-only / icon / i18n rules live in [`frontend.md`](./frontend.md), not repeated here. This section is "what exists and how to choose."

### 6.1 Primitives — present vs. absent

The palette is pruned to what's actually imported. **Present** (16) in [`components/ui/`](../frontend/src/components/ui/):

| File | Use |
| --- | --- |
| `button.tsx` | Buttons (variants/sizes below) |
| `badge.tsx` | Status / label badges |
| `alert.tsx` | Inline alert block (`default` / `destructive`) |
| `input.tsx` / `textarea.tsx` | Text input |
| `select.tsx` | Select (trigger sizes `sm` / `md`) |
| `checkbox.tsx` / `switch.tsx` / `radio-group.tsx` | Boolean / choice controls (`switch` sizes `sm` / `default`) |
| `dialog.tsx` | Modal dialog (`DialogHeader` / `Body` / `Footer` / `Title` / `Description` helpers) |
| `dropdown-menu.tsx` / `context-menu.tsx` | Dropdown / right-click menu |
| `popover.tsx` / `hover-card.tsx` / `tooltip.tsx` | Floating layers |
| `table.tsx` | Table subcomponents |

**Absent** (commonly expected, but *not* in the codebase — don't import or assume them): `card`, `tabs`, `sheet`, `skeleton`, `progress`, `accordion`, `collapsible`, `sonner` (the `Toaster` is imported straight from `sonner`), `command` (there's a custom command palette), `form` (no form library — see §9).

> **No `card.tsx`.** A "card" is the inline convention `rounded-lg border border-border bg-card p-4` (raised) — compose it directly; don't add a card primitive without cause. **No `skeleton`/`progress`** — see §9 for the real loading conventions. Need a missing primitive back? Re-add it from shadcn rather than hand-rolling.

### 6.2 Button variants / sizes

Source: [`button.tsx`](../frontend/src/components/ui/button.tsx).

- **variant:** `default` (solid `bg-primary`), `destructive`, `outline`, `secondary`, `ghost`, `link`
- **size:** `default` (`h-9`), `xs` (`h-6`), `sm` (`h-8`), `lg` (`h-10`), `icon` (`size-9`), `icon-xs` (`size-6`), `icon-sm` (`size-8`), `icon-lg` (`size-10`)

A bare `<svg>` child auto-sizes to `size-4` (`size-3` at `xs`/`icon-xs`). The `default` variant is `bg-primary text-primary-foreground` — `primary` *is* the solid fill here (unlike a separate "primary-background"); reserve `text-primary`/`border-primary` for accent semantics.

```tsx
import { Button } from "@/components/ui/button";
import { Plus } from "lucide-react";

<Button>Create</Button>                                   {/* primary action */}
<Button variant="outline">Cancel</Button>                 {/* secondary action */}
<Button variant="destructive">Delete</Button>             {/* dangerous action */}
<Button variant="ghost" size="icon-sm"><Plus /></Button>  {/* icon button */}
```

### 6.3 Badge variants

Source: [`badge.tsx`](../frontend/src/components/ui/badge.tsx). Variants: `default` (`bg-primary`), `secondary`, `destructive`, `outline`, `ghost`, `link`. Badges are `rounded-full`, size-fixed (no size prop), with an auto `size-3` leading svg. For **agent run state**, prefer `StatusPill` (§6.4) over a hand-styled badge.

### 6.4 Agent & status primitives

Project blocks in [`components/agentre/primitives.tsx`](../frontend/src/components/agentre/primitives.tsx) — reuse these for any agent/status surface (Principle 1):

| Component | Use |
| --- | --- |
| `AgentAvatar` | Agent identity avatar. Sizes `sm`/`md`/`lg` (`size-6`/`8`/`10`). Renders, in priority: a custom image (`avatarDataUrl`), a registry icon (`avatarIcon`), else initials — on the `agentColorClassNames[color]` fill with white text. Defaults: `color="agent-1"`, `size="md"`. |
| `StatusDot` | Colored run-state dot. Sizes `xs`/`sm`/`md` (`size-1.5`/`2`/`2.5`); `aria-label` = `"<status> status"`. |
| `StatusPill` | Dot + uppercase label pill — `font-mono text-2xs`, `rounded-sm`, on the status' tinted bg. The canonical "agent is RUNNING/WAITING/…" chip. |
| `SidebarButton` | Icon rail button — `ghost`/`icon`, `size-10 rounded-lg`, `sidebar-icon` color; active = `bg-primary-soft text-sidebar-icon-active shadow-xs`; ships its own hover/focus tooltip (300ms hover delay) and `aria-current`. |
| `DeviceTag` | Local/remote device chip (online/offline). |

> Other high-reuse `components/agentre/` blocks worth knowing before building: `code-block` (highlighted code + copy), `markdown-text` (agent markdown renderer), `resizable-sidebar` (drag-resized, persisted sidebar shell, §7), `thinking-block` / `compact-history-fold` (collapsible transcript regions), `app-dialog` (generic dialog host), `icon-picker`, `remote-fs-picker`. Search [`components/agentre/`](../frontend/src/components/agentre/) for a composed block before hand-rolling.

### 6.5 Toasts & notifications

Two distinct systems — use the right one:

- **Transient toasts → Sonner.** `<Toaster position="bottom-right" richColors theme={effectiveTheme} />` is mounted once in `AppLayout` ([`App.tsx`](../frontend/src/App.tsx)). Business code imports `toast` **directly from `sonner`** (`toast.success/error(title, { description, duration })`) — there is no `notify` wrapper. The toast colors are bound to design tokens in [`globals.css`](../frontend/src/styles/globals.css) (`[data-sonner-toaster][data-rich-colors="true"]`): success→`status-running-bg`, error→`destructive-soft`, warning→`status-waiting-bg`, info→`primary-soft`; neutral `foreground` text, saturated icon. See [`lib/clipboard-toast.ts`](../frontend/packages/agentre-ui/src/lib/clipboard-toast.ts) for the canonical call.
- **Agent turn-completion → the notification viewport.** `<NotificationToastViewport />` (custom, backed by a Zustand store [`stores/notification-toast-store.ts`](../frontend/src/stores/notification-toast-store.ts)) surfaces turn done / error / awaiting-approval events — up to 5 at once. Use it for *session lifecycle* signals, not generic feedback.

### 6.6 Selection guidance

- **Confirmation:** dangerous / irreversible → a `Dialog` confirm (e.g. `delete-project-dialog`, `quit-confirm-dialog`); state the blast radius in the copy. For easily reversible actions, prefer acting immediately + a Sonner toast over a blocking dialog — fewer interruptions.
- **Transient panels:** anchored small layer → `Popover` / `DropdownMenu` / `HoverCard`; right-click affordance → `ContextMenu`. (No `Sheet` primitive — a side drawer like `project-settings-drawer` is composed directly.)
- **Cards:** inline `rounded-lg border bg-card` (§6.1), not a primitive.
- **Feedback:** transient → Sonner toast; session lifecycle → notification viewport; persistent / in-page → §9 state patterns.

---

## 7. Layout & desktop shell

### The one frame

Every screen renders inside `AppLayout` ([`App.tsx`](../frontend/src/App.tsx)). Top to bottom:

1. **Title bar** (`AppTopBar`, [`chrome.tsx`](../frontend/src/components/agentre/chrome.tsx)) — `h-11`, `bg-rail`, `.wails-drag`; holds the app name/breadcrumb. Platform-aware: macOS reserves the 68px native traffic-light inset; Windows renders `WindowsWindowControls`.
2. **Icon rail** — `<aside>` `w-14`, `bg-rail`, a column of `SidebarButton`s (Chat / Projects / Issues / Org / Hooks), with the theme toggle + Settings pinned to the bottom via `mt-auto`.
3. **Context sidebar** *(per page)* — optional `ResizableSidebar` (chat agent list, project tree). `hidden lg:flex`, drag handle, width persisted.
4. **Outlet / chat panel** — the routed page (`Outlet`), or the chat **tab strip** (`h-[38px]`, drag-reorderable, `.scrollbar-none`) + `ChatPanelHost`, toggled via `display:none` by `data-page-has-chat`.
5. **Status bar** (`AppStatusBar`) — `h-7`, `bg-rail`; agent summary, connection status, version.

Swap the content in the `Outlet`/panel — keep the frame.

### Wails window chrome

- **Drag regions:** the title bar is `.wails-drag` (`--wails-draggable: drag`); interactive children opt out with `.wails-no-drag`. Double-clicking the bar calls `WindowToggleMaximise()` (unless the target is `no-drag`). Window controls call `WindowMinimise()` / `WindowToggleMaximise()` / `Quit()`.
- **Window size** is persisted to `localStorage["agentre.windowSize"]`, clamped to min `860×640`. Don't assume a viewport narrower than the minimum.

### `user-select` discipline

The body defaults to `user-select: none` (this is an app chrome, not a document). Text selection is **opted into**: `input` / `textarea` / `[contenteditable]` and any region marked `data-selectable-text="true"` get `user-select: text` (and their nested buttons/icons opt back out). When you build a panel whose text the user should be able to copy (transcripts, code, errors), mark it `data-selectable-text="true"`; for a control whose label should be copyable, use `data-copyable-control-text="true"`.

### Auto-hiding scrollbars

Scrollbars are invisible until you scroll. `useAutoHideScrollbars` ([`App.tsx`](../frontend/src/App.tsx)) sets the `--sb-thumb` CSS variable to a visible color on scroll and clears it after ~900ms idle. This is a **CSS-variable** mechanism on purpose: WKWebView doesn't repaint `::-webkit-scrollbar` on class/selector changes (WebKit bug #104412), but it does on custom-property value changes. So: **don't** restyle scrollbars with classes per container; rely on the global rules. To fully hide one (a horizontal strip like the tab list), add `.scrollbar-none`.

### No mobile — desktop only

There is **no `useIsMobile`, no `MOBILE_BREAKPOINT`, and no mobile re-shell.** Responsive utility adaptations (`sm:` / `md:` / `lg:`) do exist across the desktop chrome and pages to manage a resizable window; they rearrange or hide secondary desktop UI, not create a phone layout. Design for the desktop minimum and resizing behavior; don't add a separate mobile branch.

### Long lists

The chat transcript can hold thousands of rows and is windowed with **`@tanstack/react-virtual`** ([`chat.tsx`](../frontend/src/components/agentre/chat.tsx)): dynamic per-row size estimation, overscan, `anchorTo: "end"` stick-to-bottom, and `measureElement` for real heights (see [`transcript-rows.ts`](../frontend/packages/agentre-ui/src/transcript/transcript-rows.ts) + [`transcript-row-view.tsx`](../frontend/packages/agentre-ui/src/transcript/transcript-row-view.tsx)). Other lists (issues, org chart, settings) are bounded and render plainly — don't add virtualization unprompted, but **do** virtualize any new unbounded list rather than mounting every row.

### Layering (z-index)

Use a fixed ladder — don't invent magic numbers:

| Layer | Class | What lives here |
| --- | --- | --- |
| Base content | *(default)* | Normal page flow |
| Sticky chrome | `z-10`–`z-20` | Title bar / status bar / tab strip / sidebar drag handle |
| Floating layers | `z-50` | `Dialog`, `DropdownMenu`, `Popover`, `HoverCard`, `Select`, `Tooltip`, the rail tooltip — the shadcn/Radix default; leave it |
| Toast | *(owned by Sonner / the notification viewport)* | Portals above everything; never hand-roll a layer above it |

Ties break by DOM/portal order, not a bespoke number. A new "always on top" need usually means the element should be a real floating primitive (Dialog/Popover) that already portals correctly.

---

## 8. Motion

**Sources:** `tw-animate-css` (`@import` in [`globals.css`](../frontend/src/styles/globals.css) — provides `animate-in/out`, `fade-*`, `zoom-*`, `slide-*`) + Radix `data-state` + one custom keyframe (`typing-dot`). **No Framer Motion** — all motion is CSS.

### How to add motion that stays friendly

- **Fast and light:** micro-interactions `150ms` `ease-out`; small layout entrances `200ms` (`animate-in fade-in slide-in-from-bottom-1 duration-200 ease-out`).
- **Hover/focus via CSS pseudo-classes, not React state** — a Constraint 1 rule.
- **Enter/leave via Radix `data-state`** — `data-[state=open]:animate-in … data-[state=closed]:animate-out` with `fade-*`/`zoom-in-95` + `zoom-out-95`/`slide-*` (see `dialog.tsx`, `dropdown-menu.tsx`, `hover-card.tsx`). Don't hand-roll show/hide with `setTimeout`.
- **Prefer `transition-colors`/`transition-transform` over `transition-all`** — animate only what should move. Common: chevron rotation `transition-transform duration-150`, collapse via `transition-[grid-template-rows] duration-150`.
- **Spinners:** `animate-spin` on a `lucide` spinner (`Loader2` / `RefreshCw` / `LoaderCircle`), sized to context (`size-3.5`/`size-4` inline), usually tinted to the active status.
- **Honor reduced motion explicitly.** Agentre does **not** rely on a global `prefers-reduced-motion` reset — instead each animation carries a `motion-reduce:animate-none` / `motion-reduce:transition-none` (or `motion-safe:`) modifier. **Always add one** when you introduce motion; it's how this codebase stays reduced-motion-safe.

### Available animations

| utility / pattern | Use |
| --- | --- |
| `animate-typing-dot` (`--animate-typing-dot`) | The chat typing/compacting indicator — three dots with staggered `[animation-delay:…]` (see `TypingIndicator` in [`transcript-row-view.tsx`](../frontend/packages/agentre-ui/src/transcript/transcript-row-view.tsx)). Keyframe `typing-dot` defined in [`globals.css`](../frontend/src/styles/globals.css). |
| `data-[state=open]:animate-in … fade-*/zoom-in-95/zoom-out-95/slide-*` | Dialog / Dropdown / Popover / HoverCard enter-leave (Radix-driven) |
| `animate-spin` | `Loader2` / `RefreshCw` / `LoaderCircle` spinners |
| `transition-colors` / `transition-transform` / `duration-150` | hover/focus color, chevron rotation |

```tsx
// Typing indicator dot (respects reduced motion)
<span className="size-1.5 rounded-full bg-muted-foreground animate-typing-dot motion-reduce:animate-none" />

// Floating layer enter/leave (Radix data-state + tw-animate-css)
<div className="data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95
                data-[state=closed]:animate-out data-[state=closed]:fade-out-0 duration-150">…</div>
```

---

## 9. State patterns

Every async flow covers loading / empty / error / success consistently. **Important:** unlike some shadcn apps, agentre has **no shared `StateScreen` / `EmptyState` / `LoadingState` / `Skeleton` / `Progress` components yet** — these states are currently composed ad hoc. Follow the conventions below, and **when a state block repeats across two or more pages, extract a shared component** rather than copy-pasting (Principle 6).

| State | Convention today |
| --- | --- |
| **Loading** | A centered `Loader2` (`animate-spin`, `text-muted-foreground`) for whole regions, or an inline `Loader2 size-3.5 animate-spin` inside a disabled button for single actions. For lightweight "first load" copy, the `CenterNote` pattern (centered `text-xs text-muted-foreground`, e.g. [`issues-page.tsx`](../frontend/src/components/agentre/issues-page.tsx)). No skeletons exist — add one only if you build the shared component. |
| **Empty** | Centered icon (`Inbox` / `Sparkles` in `decorative-foreground`/`primary-soft`) + `text-sm font-semibold` title + `text-xs text-muted-foreground` description + a primary CTA. See `ProvidersEmptyState` ([`llm-providers.tsx`](../frontend/src/components/agentre/llm-providers.tsx)) and `IssuesEmpty` ([`issues-page.tsx`](../frontend/src/components/agentre/issues-page.tsx)). |
| **Error** | `ErrorCard` ([`transcript-row-view.tsx`](../frontend/packages/agentre-ui/src/transcript/transcript-row-view.tsx)): `border-status-error/40 bg-destructive-soft`, `TriangleAlert` icon in `text-status-error`, the message, and an optional outline retry button. For page-level failures, centered `text-destructive` copy. |
| **Success** | Transient → a Sonner `toast.success` (§6.5); a completed agent turn → the notification viewport. |
| **In-progress** | No general-purpose `Progress` primitive in `components/ui/`. Agent background-task progress has a dedicated `TaskProgressBar` ([`task-progress/`](../frontend/src/components/agentre/task-progress/)) — a real bar + expandable task list with a `LoaderCircle` spinner tinted `text-status-waiting`. For other waits, a status-tinted spinner + readable copy. |

### Forms & validation

Forms are plain `useState` + controlled shadcn components — **no react-hook-form / zod**, no `form.tsx`. Keep new forms on this pattern unless asked. Guidance:

- **Validate late, forgive early.** Don't error while a field is being filled; validate on blur/submit, then switch to live revalidation so the message clears the instant it's fixed.
- **Error sits with the field:** a short `text-destructive text-xs` line under the input, mark the control (`aria-invalid`). For a failed *submit*, raise a Sonner error toast (there is no `Alert`-banner convention for form-level errors).
- **Don't lose input on failure** — a failed save keeps every field as-is.
- **Submit in flight:** disable the button + inline `Loader2`.

### Writing & microcopy

- **Through i18n always** (`t(...)`, zh-CN + en) — Constraint, see [`frontend.md`](./frontend.md). Don't hardcode Chinese.
- **Buttons are verbs** naming the action ("Create", "Save", "Delete"), with an in-flight progress label ("Saving…").
- **Errors are specific and actionable:** what failed + why + next step, not "Something went wrong." Put raw detail in a mono region, not the headline.
- **Sentence case** for buttons/titles/labels; product names keep their casing. Status labels are the exception — uppercase (`RUNNING`).

### Interactive states

- **Hover/focus** via CSS pseudo-classes, never React state (§1, §8).
- **Disabled:** the shadcn primitives already apply `disabled:opacity-50 disabled:pointer-events-none` — reuse them; give a non-obvious disable a reason nearby.
- **Selected / current:** persistent selection uses `accent` / `sidebar-active-bg` fills or `primary` text, paired with a non-color cue (`aria-current`, an indicator) — distinct from transient `hover:accent` (§10).

The rule: **no silent operations** — after any action the user can see success / failure / in-progress.

---

## 10. Accessibility

Friendly UX includes keyboard, screen-reader, low-vision, and motion-sensitive users. Verify these alongside the both-themes check.

### Contrast

- **Target WCAG AA:** ≥ 4.5:1 for normal text, ≥ 3:1 for large text and meaningful UI/icon edges. `foreground`, the status `*-fg` pairs, and brand `primary-text` pass comfortably.
- **`muted-foreground` is the floor for readable text** — secondary/descriptive copy lives there; use `foreground` for anything dense or critical. `decorative-foreground` is fainter still and is **not** a text color: it is for glyphs that carry no information (§3.1). Both are guarded per-surface, so "it looks fine on a card" is no longer the test.
- **Never encode meaning in color alone.** The agent palette is *identity*, not status — always pair an agent color with its name/initials, and every status color with its label/icon (`StatusPill` does both).

### Focus visibility

The base layer applies `outline-ring/50` and the shadcn primitives rely on `focus-visible:ring-ring/50`. The cost: **any custom interactive element you build has no visible keyboard focus unless you add the ring yourself.** Every custom clickable (a `div`/`span` with `onClick`, a bespoke card action) must add `focus-visible:ring-2 focus-visible:ring-ring/50` and be reachable (real `<button>`/`<a>`, or `tabIndex={0}` + key handlers). Don't strip focus styling to "clean up" a layout.

### Keyboard & screen readers

- **Everything actionable is reachable and operable by keyboard** — prefer native `<button>`/`<a>`/`<input>`; the Radix primitives (Dialog, DropdownMenu, Popover, Select…) already ship focus trap, arrow-key nav, Esc, and return-focus — a reason to reuse them over hand-rolled overlays (§6).
- **Icon-only controls need an accessible name:** `aria-label` on every icon `Button` (`SidebarButton` already does this).
- **Announce async state:** loading/error/progress regions carry `role="status"` + `aria-label` (the `TypingIndicator` already uses `role="status"` + `aria-live="polite"`) so non-visual users get the same "no silent operations" guarantee (§9).
- **Decorative icons** (next to a text label, or a purely visual avatar) are `aria-hidden` so they aren't double-announced.

### Reduced motion

There's no global reset — honor it **per-utility** with `motion-reduce:` / `motion-safe:` modifiers on every animation/transition (§8). Don't bypass it with JS tweens.

### Accessibility checklist

- [ ] Text meets AA contrast on **both** themes; meaning never carried by color alone (agent color is identity, not state).
- [ ] Every custom interactive element is keyboard-reachable and shows a visible `focus-visible` ring.
- [ ] Icon-only buttons have `aria-label`; decorative icons/avatars are `aria-hidden`.
- [ ] Async/loading/error regions expose `role` + `aria-label`.
- [ ] Motion still works (and calms down) under `prefers-reduced-motion` via `motion-reduce:`.

---

## 11. New-page / block recipe

When building a new page or dialog, run this checklist to stay consistent:

- [ ] **Shell:** render inside the existing `AppLayout` frame — a routed `Outlet` page (title bar / rail / status bar are given). Add a `ResizableSidebar` only if the page needs a list/detail split (§7).
- [ ] **Color** entirely from tokens (`bg-card` / `text-foreground` / `border-border` / `text-primary` / `bg-primary` …), no literals, verified on both themes (Constraints 1–2, §3–4).
- [ ] **Agent/status surfaces** reuse `AgentAvatar` / `StatusDot` / `StatusPill` and the `agentColorClassNames` / `tokenToCssColor` helpers — never re-derive agent color or re-style status (Principle 1, §3.5–3.6, §6.4).
- [ ] **Components** reuse first — search [`components/agentre/`](../frontend/src/components/agentre/) and [`components/ui/`](../frontend/src/components/ui/) before building; remember there's **no `card`/`tabs`/`sheet`/`skeleton`/`progress`** primitive — compose the inline card and the §9 state patterns; variants via CVA, classes via `cn()`, icons via `lucide-react` (§6).
- [ ] **Cards** are inline `rounded-lg border border-border bg-card p-4`; pick the surface by elevation (§3.12) and pair with the matching radius.
- [ ] **State:** loading / empty / error / success all covered, never silent (§9); extract a shared state block if it repeats.
- [ ] **Motion** restrained (`150–200ms`, `ease-out`), hover/focus via pseudo-classes, enter/leave via `data-state`, and **a `motion-reduce:` modifier on every animation** (§8).
- [ ] **Depth** uses the surface ladder (§3.12) and the z-index ladder (`z-10`–`z-20` chrome / `z-50` floating, §7) — keep borders in dark, no magic `z-[…]`.
- [ ] **Long lists** virtualize with `@tanstack/react-virtual` if unbounded (§7).
- [ ] **Accessibility:** AA contrast on both themes; meaning never color-only; custom controls keyboard-reachable with a visible focus ring; `aria-label` on icon buttons; reduced-motion-safe (§10).
- [ ] **Copy** through i18n (zh-CN + en), sentence-case, verbs on buttons, specific errors (§9, [`frontend.md`](./frontend.md)).
- [ ] **`user-select`:** mark copyable text regions `data-selectable-text="true"` (the body defaults to no-select, §7).

Page skeleton (tokens + existing primitives + the routed-page pattern):

```tsx
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";

export default function ExamplePage() {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col bg-background text-foreground">
      {/* page header (the window title bar is provided by AppLayout) */}
      <header className="flex h-11 shrink-0 items-center border-b border-border px-4">
        <h1 className="text-sm font-semibold">{t("example.title")}</h1>
      </header>

      {/* scroll region — global auto-hiding scrollbar applies */}
      <main className="min-h-0 flex-1 overflow-y-auto px-4 py-4" data-selectable-text="true">
        <section className="mx-auto w-full max-w-[864px] space-y-3">
          <div className="rounded-lg border border-border bg-card p-4">…</div>
        </section>
      </main>

      {/* action bar */}
      <footer className="flex shrink-0 justify-end gap-2 border-t border-border px-4 py-3">
        <Button variant="outline">{t("common.cancel")}</Button>
        <Button>{t("common.confirm")}</Button>
      </footer>
    </div>
  );
}
```

---

## 12. Sources & verification

**Implementation source of truth (read/edit these when changing the design):**

- Color / font / motion / scrollbar tokens → [`frontend/src/styles/globals.css`](../frontend/src/styles/globals.css); code highlighting → [`code-highlight.css`](../frontend/packages/agentre-ui/styles/code-highlight.css)
- Theming + the app shell (title bar, rail, status bar, auto-hide scrollbars, window size) → [`frontend/src/App.tsx`](../frontend/src/App.tsx) + [`frontend/src/components/agentre/chrome.tsx`](../frontend/src/components/agentre/chrome.tsx)
- Agent color / status model → [`frontend/src/components/agentre/types.ts`](../frontend/src/components/agentre/types.ts) + [`session-avatar.ts`](../frontend/src/components/agentre/session-avatar.ts); agent/status primitives → [`primitives.tsx`](../frontend/src/components/agentre/primitives.tsx)
- Component primitives → [`frontend/src/components/ui/`](../frontend/src/components/ui/); shadcn config → [`components.json`](../frontend/components.json); `cn()` → [`frontend/src/lib/utils.ts`](../frontend/src/lib/utils.ts)

**Related docs:** UI hard rules (shadcn-only, i18n, lint, commit flow) → [`frontend.md`](./frontend.md); layering / dependency direction / storage → [`architecture.md`](./architecture.md); TDD / SOLID → [`develop.md`](./develop.md); test design → [`testing.md`](./testing.md); doc fact-checking → [`documentation.md`](./documentation.md).

> When editing this doc, follow [`documentation.md`](./documentation.md): stage the intended change, derive `VERIFY_TREE="$(git write-tree)"`, and verify token values, component names, and variants against that proposed tree; enumerate counts and lists rather than trusting memory.
