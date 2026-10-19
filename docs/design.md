# Agentre Design System

> **A reuse-oriented design reference.** It consolidates the visual language provided by the shared token definitions in [`frontend/packages/agentre-ui/styles/tokens.css`](../frontend/packages/agentre-ui/styles/tokens.css), the host stylesheet, and the shadcn + `components/agentre` layers into one place you can copy from: **color tokens (full light/dark values), the theming mechanism, the component palette, the desktop window shell, motion, state patterns, accessibility, and an end-to-end new-page recipe.** Read this before building any new page, dialog, or block so it stays visually and behaviorally consistent with the rest of the app.

> **Stack in one line:** React 19 + shadcn/ui (Radix primitives, `new-york` style) + Tailwind CSS v4 + React Router (MemoryRouter) inside a **Wails desktop window**. Token values live in the shared package's [`styles/tokens.css`](../frontend/packages/agentre-ui/styles/tokens.css), imported by the host [`globals.css`](../frontend/src/styles/globals.css); **class names have no prefix** (`bg-background`, not `tw-bg-background`); `cn()` lives at [`frontend/packages/agentre-ui/src/lib/utils.ts`](../frontend/packages/agentre-ui/src/lib/utils.ts); `baseColor` is `neutral` ([`components.json`](../frontend/components.json)).

---

## 0. What this doc owns

| Owned here | Owned elsewhere |
| --- | --- |
| Color-token values, semantics, usage; the agent palette & status system | The enforced rules (shadcn-from-`@agentre-hub/agentre-ui`-only, i18n, `lucide`/Iconify icons, lint, no hardcoded Chinese) → [`frontend.md`](./frontend.md) |
| Theming mechanism, `dark:` usage, the desktop window shell | Layering / dependency direction, `internal/app` ↔ service ↔ repository, storage paths → [`architecture.md`](./architecture.md) |
| Component palette, variants, selection guidance | TDD / SOLID / commit style → [`develop.md`](./develop.md); test design → [`testing.md`](./testing.md) |
| Elevation (surfaces & shadows), z-index, motion, state patterns, accessibility, page recipe | Doc fact-checking discipline → [`documentation.md`](./documentation.md) |

This doc restates the [`frontend.md`](./frontend.md) hard rules only where needed, then links back — it does not duplicate them.

---

## 1. Core Constraints (non-negotiable)

Every UI change must satisfy all of these. They are the bar for "consistent, friendly UI/UX" in this codebase.

- **Use tokens, not literal colors — one value, one place.** Never write a hex (`#3b6896`), an `rgb()`, or a palette class (`text-blue-500`). Always use a semantic token — `bg-background`, `text-foreground`, `bg-sheet`, `text-primary`, `text-primary-text`, `text-muted-foreground`, … (§3). All color values live in exactly one place — the shared [`tokens.css`](../frontend/packages/agentre-ui/styles/tokens.css) — so the palette stays unified and a single edit re-skins everything. One semantic concept maps to **one** token: before adding a color, check §3 for an existing token and reuse it; don't introduce a near-duplicate. Only add a new token when the concept is genuinely new — with both light and dark values — and document it in §3.

  > **Sanctioned literal-color exceptions** (everything else must be a token): the xterm ANSI
  > palette in [`terminal/terminal-theme.ts`](../frontend/packages/agentre-ui/src/terminal/terminal-theme.ts)
  > (xterm.js can't consume CSS variables); the `#94a3b8` slate **avatar fallback** when agent meta is
  > missing (§3.6); and neutral black-alpha **shadows/scrim** (`box-shadow rgba(0,0,0,…)`, the `Dialog`
  > backdrop) — there are no `--shadow-*` tokens by design (§3.12).
- **Both themes, always.** Light and dark are first-class. Because every color comes from a token that has a `:root` and a `.dark` value, using tokens makes a component theme-correct for free. Verify on real light *and* dark before considering anything done (§4).
- **This is a desktop window, not a web page.** The shell is a fixed Wails frame: the title bar, icon rail, each page's left list, the chat tab strip, the chat right panel and the status bar all sit on one `chrome` surface, and the workspace is a rounded **content sheet** floating on it; settings is a dialog over the current page (§7). `html, body, #root` are `height:100%; overflow:hidden`, and the body defaults to `user-select:none`; text selection is **opted into** per region, not the default (§7). There is **no mobile breakpoint** and no `useIsMobile` — design for a resizable desktop window (min `860×640`), not a phone.
- **No inline `style={{}}` for what Tailwind can express.** Compose utility classes via `cn()` (`clsx` + `tailwind-merge`); build variants with `class-variance-authority` (CVA). Inline styles only for genuinely dynamic values (a computed width, a per-agent `var(--agent-N)` color, a `display` toggle).
- **Hover/focus are CSS, not state.** Express interactive visuals with pseudo-classes (`hover:bg-accent`, `focus-visible:focus-outline`). React state is for data/logic, not styling.
- **Reuse components before building new ones.** Default to the shadcn primitives in [`frontend/packages/agentre-ui/src/ui/`](../frontend/packages/agentre-ui/src/ui/) (§6) and the project blocks in [`frontend/src/components/agentre/`](../frontend/src/components/agentre/); icons come from `lucide-react` (with `@iconify/react` for the agent icon registry) — don't hand-roll a control that already exists. When the same block appears in two or more places, extract one shared component instead of copy-pasting.
- **Keep motion restrained, and honor reduced motion.** Enter/leave in `150–200ms`, `ease-out`; reuse `tw-animate-css` utilities and Radix `data-state` rather than inlining keyframes; prefer `transition-colors`/`transition-transform` over `transition-all`. Every animation carries a `motion-reduce:` (or `motion-safe:`) modifier (§8).
- **No silent operations.** Every async flow surfaces loading / empty / error / success. The user must always know whether their action worked (§9).
- **All visible UI copy goes through i18n.** New text uses `react-i18next`'s `t(...)` and updates both `frontend/src/i18n/locales/zh-CN/` and `.../en/` (domain modules merged by `index.ts`). Do not hardcode Chinese (ESLint blocks it). Do **not** translate dynamic agent / user / terminal / markdown output. This is a [`frontend.md`](./frontend.md) hard rule — see it for details.
- **Don't introduce new colors or fonts ad hoc.** New color → add a token in the shared [`tokens.css`](../frontend/packages/agentre-ui/styles/tokens.css) (with both light and dark values) and document it here. New font → add a `--font-*` token; don't reference an unconfigured family.

---

## 2. Design Principles

The "why" behind the constraints — apply these when shaping a screen.

1. **Agent identity is visual.** Agentre coordinates many concurrent coding agents, so an agent must be recognizable at a glance. Identity is carried by **a 16-color agent palette** (`--agent-1…16`, §3.6) seeded from the agent's color token, and run state by **four status tokens** (`running` / `waiting` / `idle` / `error`, §3.5) rendered as dots and pills. Reuse `AgentAvatar` / `StatusDot` / `StatusPill` — don't re-derive agent color or re-style status.
2. **System state is always visible.** No silent work. Each async flow shows loading → progress → result (toast or in-place state, §9). A turn that finishes, errors, or needs approval also raises an app notification (§6.5).
3. **Color is semantic, never decorative.** The steel-blue `primary` = interactive / brand; `status-running` green = running / success; `status-waiting` amber = needs attention; `status-error` / `destructive` red = error / dangerous. The agent hues are **identity**, not status — never read meaning into *which* agent color a session has.
4. **One desktop frame.** Every screen lives in the same shell — custom title bar, a `w-14` icon rail, an optional resizable sidebar, the chat tab strip and a status bar, all on `chrome`, around one content sheet (§7). Swap the content in the `Outlet`/panel, not the frame.
5. **Depth is a surface step, not a line.** Regions are told apart by the surface ladder — `chrome` (the frame) → `sheet` (the workspace) → `raised` (a selected item on the frame) — and, inside a surface, by spacing and fills (§3.1, §3.12). **No structural lines:** two regions on the same surface are never divided by a border (§3.4, lint-enforced). Shadows are a faint accent at most, because they barely render on dark surfaces.
6. **High cohesion, low coupling.** Each UI unit has a single purpose, a clear interface, and is understandable and testable on its own. A file growing large is usually a signal to split it.

---

## 3. Color Tokens (full light / dark values)

**Single source:** shared token definitions in [`frontend/packages/agentre-ui/styles/tokens.css`](../frontend/packages/agentre-ui/styles/tokens.css). The host [`globals.css`](../frontend/src/styles/globals.css) imports them and exposes the Tailwind mapping; `:root` defines light, `.dark` overrides for dark, so `bg-<token>` / `text-<token>` / `border-<token>` all switch with the theme automatically.

**Usage:**
- Background `bg-<token>`, text `text-<token>`, border `border-<token>`. Borders are not for separating regions — see §3.4 for the few lines that remain. Keyboard focus is not assembled from `ring-*` — see §10.
- Opacity modifiers compose directly: `bg-primary/90`, `ring-destructive/20`, `border-status-error/40`.
- **Never hard-code a color value** — see Constraint 1. For a dark-only tweak use the `dark:` variant.

### 3.1 Base surfaces & text hierarchy

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `background` | `#fafafa` | `#18191b` | Base page background. The shell itself paints `chrome` and the workspace paints `sheet` (below) |
| `foreground` | `#18181b` | `#e8e8e9` | Primary text |
| `card` | `#ffffff` | `#202122` | Dialog surface (`Dialog`, `DialogShell`). In-page cards take a fill instead (§6.1) |
| `card-foreground` | `#18181b` | `#e8e8e9` | Text on cards |
| `popover` | `#ffffff` | `#292a2e` | Floating layers (dropdown / tooltip / toast) surface |
| `popover-foreground` | `#18181b` | `#e8e8e9` | Text in floating layers |
| `preview-canvas` | `#ffffff` | `#ffffff` | Canvas under a rendered HTML page in the file preview. White in both themes because pages are written against the browser's white default |
| `chrome` | `#ececee` | `#111214` | **The frame**: title bar, icon rail, each page's left list (session list, org index column, Hooks list), the chat tab strip, the chat right panel, the status bar, the settings dialog's nav column. `ResizableSidebar` paints it by default (`surface="chrome"`) |
| `sheet` | `#ffffff` | `#1b1c1f` | **The content sheet**: the workspace (`content-sheet`, below), the settings dialog's content area, the active chat tab, a focused field |
| `raised` | `#ffffff` | `#27282c` | **A selected item on the frame**: the selected session row, the selected org index row and department header, the current icon-rail item, the Hooks selected row, the current outline entry in the right panel |
| `field-fill` / `field-fill-hover` | `foreground` 5% / 7% | same | Fields at rest / on hover (`bg-field-fill`, `hover:bg-field-fill-hover`); also cards, code blocks and settings groups. `field-fill-hover` is also the hover of an unselected row in the frame's lists (session rows and group headers — including the desktop session list's agent group header — org index rows and department headers): on `chrome` it is 1.15 / 1.16 from the frame and 1.36 / 1.09 from the selected `raised` (light / dark), guarded by [`chrome-row-hover.test.tsx`](../frontend/packages/agentre-ui/src/chrome-row-hover.test.tsx) and, for the desktop-only agent group header, [`host-row-hover.test.tsx`](../frontend/src/components/agentre/__tests__/host-row-hover.test.tsx). The session index's filter chips (on `chrome`) take the same pair: an unpressed chip rests on `field-fill` (1.11 from the frame, 1.31 / 1.15 from `raised`) and hovers to `field-fill-hover`; the pressed chip is `primary-soft` (1.18 / 1.12 from a resting chip), guarded by the same `chrome-row-hover.test.tsx`. A `color-mix` of `foreground` with transparent, so it is always one step darker than whatever it lands on — sheet, a filled group or the chrome |
| `button-fill` / `button-fill-hover` | `foreground` 8% / 12% | same | `outline` and `secondary` buttons, small filled controls (§6.2) |
| `fill-muted-foreground` | `#5a5a61` | `#abaeb4` | **Muted text on a fill.** A fill darkens the surface under it and `muted-foreground` falls under 4.5 there (4.42 for a field on `chrome`). **You do not write this class:** the four fill utilities re-point `muted-foreground` to it for their subtree, exactly like `bg-accent` (§3.3); worst case 4.58 light / 4.59 dark. Do **not** add an opacity suffix to a fill (`bg-field-fill/50`) — that drops the re-point |
| `rail` | `#e4e4e7` | `#0b0b0c` | No band paints it any more (the frame is `chrome`); it remains in `tokens.css` and in the surface guards of [`tokens.test.ts`](../frontend/packages/agentre-ui/src/tokens.test.ts) |
| `muted-foreground` | `#65656d` | `#909399` | De-emphasized / descriptive text — timestamps, counts, metadata labels, section headings. **This is the floor for anything a user has to read.** Its value is set by the *darkest* surface it lands on, not by `card`: the status bar and window controls put it on `rail`, where the old `#71717a` was only 3.81. Now 4.90 / 6.09 on `chrome`, 5.78 / 5.53 on `sheet`, 4.55 on `rail`, 5.26 on `secondary`/`code-surface`, 5.78 on `card` (light / dark where both are given); on a fill it is swapped for `fill-muted-foreground`. Guarded per-surface by [`tokens.test.ts`](../frontend/packages/agentre-ui/src/tokens.test.ts) |
| `decorative-foreground` | `#8a8a91` | `#71757e` | **Glyphs that never carry information** — separator dots (`·` `/` `›`), diff/file line numbers, `aria-hidden` icons that merely accompany adjacent text, fallback glyphs. Now 3.43 light / 3.49 dark on `card` and 3.12 / 3.10 on `secondary`; the one place it still dips under 3:1 is the light `rail` (2.70), which is why it stays limited to glyphs that carry no information. Was named `subtle-foreground`; that name read like "a weaker body text", so 97 metadata labels quietly ended up on it (2026-08-19 audit) — they all moved to `muted-foreground`. If the thing has to be *read*, it does not belong here |

> **The surface ladder of the frame** — `chrome` → `sheet` → `raised`, in both themes. The frame and the sheet are separated by color alone, never a line: 1.18:1 light, 1.10:1 dark (`raised` on `chrome`: 1.18 / 1.27). Body text is 15.0 / 15.3 on `chrome`. The workspace is a `content-sheet` (utility in `tokens.css`): `sheet` background, `0.75rem` (12px) radius, an 8px gap to the frame on the right and bottom (`margin: 0 0.5rem 0.5rem 0` — the top meets the tab strip, the left meets the left list), `overflow: hidden`, and in light only the `--sheet-shadow` (a 0.5px ring plus a 1–3px soft drop); dark has no sheet shadow. Pick the surface that matches the layer (§3.12).

### 3.2 Brand primary (steel-blue)

A muted, cool steel-blue chosen to stay distinct from the bright agent blues (§3.6).

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `primary` | `#3b6896` | `#5b8dbf` | **Solid** brand fill (the `Button`/`Badge` `default` variant is `bg-primary text-primary-foreground`) and accent borders/indicators |
| `primary-foreground` | `#ffffff` | `#0a1420` | Text/icons on solid `primary` |
| `primary-soft` | `#eef4fa` | `#1a2738` | Soft brand wash — active sidebar items, icon backgrounds, info toasts |
| `primary-text` | `#3b6896` | `#8eb6dc` | Brand-tinted **text/icon** on a soft/neutral background (dark is brightened for contrast vs. solid `primary`) |
| `ring` | `#3b6896` | `#5b8dbf` | Keyboard focus — consumed only through `focus-outline` / `focus-outline-inset` / `focus-halo` (§10) |

> Note the split: `primary` is the solid fill, `primary-text` is the readable on-light tint. Use `text-primary-text` for brand-colored text on `primary-soft`/`card`; use `bg-primary text-primary-foreground` for a solid control.

### 3.3 Secondary / muted / accent

> `secondary` and `muted` share a value in light and differ in dark; `accent` deliberately equals no resting surface (see its row).

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `secondary` | `#f4f4f5` | `#292a2e` | Resting neutral fill (table header rows, the `DialogShell` footer band `bg-secondary/40`). Not a button fill — `outline` / `secondary` buttons use `button-fill` (§6.2) — and not the tab strip, which sits on `chrome` (§7) |
| `secondary-foreground` | `#3f3f46` | `#c6c8cb` | Text on secondary |
| `muted` | `#f4f4f5` | `#202122` | Muted background (group fills, placeholders) |
| `accent` | `#e0e0e3` | `#3c3e43` | **交互反馈** —— 内容表面上的 hover / 选中填充。刻意不等于任何静止表面：曾经是 `#f4f4f5`，与 `secondary`/`muted`/`sidebar` 同字节，86 处 `hover:bg-accent` 在那些面上渲染成 1.00:1。实测（亮 / 暗）card 1.32 / 1.51、popover 1.32 / 1.34、background 1.26 / 1.64、secondary 1.20 / 1.34。**外壳带上的 hover 用 `rail-accent`，不要用它** |
| `rail-accent` | `#f7f7f8` | `#232427` | 窗口外框带（标题栏 / 图标栏 / 状态栏，即 `bg-chrome` 之上）的 hover / focus 反馈。外框比任何内容表面都暗，**一个值无法同时服务两边**：在白色内容页上够深的填充落到外框上会被吃掉。外框是下沉的一层，所以 hover 是提亮而非压暗，与 `sidebar-active-bg` 一致。`chrome` 上 1.10 / 1.21 |
| `accent-foreground` | `#18181b` | `#e8e8e9` | Text on accent |
| `accent-muted-foreground` | `#5f5f67` | `#abaeb4` | **Muted text on the accent fill** — the description, shortcut or trailing path inside a row that is currently selected or hovered. `muted-foreground` is only 4.38 light / 3.48 dark there; this one is 4.80 / 4.81, and stays above 4.5 on the translucent hover fills (`accent/40`–`/60` over `card` / `popover`). **You do not write this class by hand:** the `bg-accent` utility re-points `muted-foreground` to it for the whole subtree, under whatever variant you used (`hover:bg-accent`, `focus:bg-accent`, `data-[selected=true]:bg-accent`), so `text-muted-foreground` inside the row simply follows. The one exception is a translucent fill — `bg-accent/60` cannot carry the re-point — where the same variant also takes `on-accent`: `hover:bg-accent/60 hover:on-accent`. Guarded by [`tokens.test.ts`](../frontend/packages/agentre-ui/src/tokens.test.ts) |

### 3.4 Edges: two tiers plus control edges

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `border` | `#e4e4e7` | `#2d2e31` | The divider color — **not for structural lines** (below). The `@layer base` reset still makes it every element's default `border-color`, so a bare `border` draws in it. Remaining uses: the menu separator (`h-px bg-border`, a fill rather than an edge) and the markdown `<hr>` an agent writes in its output |
| `border-strong` | `#d4d4d8` | `#3d3f44` | The `DialogShell` grab handle (`bg-border-strong`) and the edge of the hand-drawn radio dots (session execution target, reasoning-effort picker) — control edges, not dividers |
| `input` | `#cbcbd0` | `#4d5056` | The unchecked `Switch` track (`bg-input`). No longer an edge: the `outline` `Button` is filled (§6.2), and `border-input` is a lint error |
| `input-bg` | `#ffffff` | `#18191b` | Opaque control fill — the model-target picker's search box. Ordinary fields use `field-fill` (§3.1) |
| `field-border` | `#ababb1` | `#5f626a` | No field paints it: fields — `Input` / `Textarea` / `SelectTrigger` / `SearchInput` and the hand-assembled ones (Hooks sidebar search, board scope and filter triggers, the model-target picker, …) — are borderless, `bg-field-fill` at rest, `hover:bg-field-fill-hover`, and `bg-sheet` + the focus ring when focused or open (§10). `border-field-border` is a lint error. Guarded by [`src/ui/field-edge.test.tsx`](../frontend/packages/agentre-ui/src/ui/field-edge.test.tsx), [`src/ui/select.test.tsx`](../frontend/packages/agentre-ui/src/ui/select.test.tsx) and, for the desktop's hand-assembled fields, [`__tests__/field-edge.test.tsx`](../frontend/src/components/agentre/__tests__/field-edge.test.tsx) |
| `control-border` | `#8a8a91` | `#71757e` | **Controls whose outline *is* the control** — the unchecked `Checkbox` and the outline `Toggle`: small, with no fill and nothing else to be recognised by, so losing the edge loses the control. Sized to clear WCAG 3:1 on the worst surface a control lands on (`secondary`: 3.12 light / 3.10 dark). Do **not** reach for `border`/`input` here. Guarded by [`src/ui/checkbox.test.tsx`](../frontend/packages/agentre-ui/src/ui/checkbox.test.tsx) |

> **Divider, two fill tiers, control edge.** Fields and outline buttons left the edge ladder for the fill ladder: a field is `field-fill` (5%, 7% on hover), a button `button-fill` (8%, 12% on hover). What remains of edges is `border` for the few kept lines and `control-border` for controls whose outline *is* the control. Guarded by the `填充两档：字段与按钮` and `分隔线 / 填充两档 + 控件边` suites of [`tokens.test.ts`](../frontend/packages/agentre-ui/src/tokens.test.ts).
>
> **No structural lines.** Two regions on the same surface are never divided by a line — not between the frame bands (title bar, icon rail, status bar, sidebars, tab strip, right panel), not between a panel header and its body, not around a card or between a card's header and body, not between list rows (`divide-*`, `border-b last:border-b-0`) or table rows, not around popovers and dialogs or under their headers and above their footers, not above the composer when the transcript scrolls. They are told apart by the surface ladder, by spacing, or by a fill; after scrolling, a fade replaces the line between a title and the body. **Lines that may still appear:** the keyboard focus ring (§10); checkbox / radio / switch edges (`border-control-border`); status color bars (the amber "needs attention" stripe `border-l-2 border-status-waiting/40` in the session list, the sub-agent run group's `border-l-2 border-l-status-*`); drop-target hints (`border-2 border-dashed border-ring`). The sidebar resize handle draws nothing at rest and a thin brand bar on hover and while dragging. Lint rejects the divider colors (`border-border`, `border-*-border` other than `control-border`), `divide-*`, and field / button edges (`border-input`, `border-field-border`) — see the [enforced-rules table](./develop.md#enforced-rules-mechanically-checked).

### 3.5 Status colors (agent run state)

The heart of agentre's state language. Four states, each with a solid color (dots / fills) and — for `running`/`waiting`/`error` — a soft tinted background (pills) plus a separate color for the state written **as text**. Driven by `statusConfig` in the shared package's [`transcript/agent-status.ts`](../frontend/packages/agentre-ui/src/transcript/agent-status.ts).

| State | solid (`status-*`) Light → Dark | bg (`status-*-bg`) Light → Dark | Rendered as |
| --- | --- | --- | --- |
| `running` | `#109066` → `#34d399` | `#ecfdf5` → `#0f2218` | green dot / pill — agent is working |
| `waiting` | `#b07513` → `#fbbf24` | `#fdfaed` → `#261d0d` | amber dot / pill — awaiting approval/input |
| `idle` | `#84848b` → `#747880` | *(uses `secondary`)* | gray dot; text falls to `muted-foreground`. Sized against the darkest surface the dot sits on, the session list on `chrome` (3.15 light / 4.23 dark; 3.71 / 3.85 on `sheet`) |
| `error` | `#dc2626` → `#f87171` | *(uses `destructive-soft`)* | red dot / pill — turn failed; its text is `destructive-text` (§3.11) |

**Each state has up to four roles — do not mix them up:**

| Role | Token | Renders as |
| --- | --- | --- |
| fill | `status-<state>` | dots, solid badges, progress |
| soft fill | `status-<state>-bg` | the pill background |
| on-fill text | `status-<state>-foreground` | text sitting **on** the saturated fill |
| as text | `status-<state>-text` | the state rendered **as text** (on `status-*-bg` or a card) |

| Token | Light | Dark | Why it exists |
| --- | --- | --- | --- |
| `status-running-foreground` | `#04140c` | `#04140c` | Text on the solid green |
| `status-waiting-foreground` | `#04140c` | *(same)* | Near-black ink on the amber fills. Both themes keep an amber fill — light `#b07513` (4.86) and dark `#fbbf24` (11.33) — so one value reads on either |
| `status-running-text` | `#0c7053` | `#34d399` | The saturated fill is unreadable **as text** in light: `#109066` on its own pill is 3.83. Dark already cleared the bar, so it reuses the fill value |
| `status-waiting-text` | `#aa5413` | `#fbbf24` | Same story: `#b07513` on its own pill is 3.72 |

The `-text` split is guarded by [`packages/agentre-ui/src/tokens.test.ts`](../frontend/packages/agentre-ui/src/tokens.test.ts) (≥4.5 on both the pill and `card`, both themes), and the pairs `statusConfig` actually picks by [`transcript/agent-status.test.ts`](../frontend/packages/agentre-ui/src/transcript/agent-status.test.ts). Render status only through `StatusDot` / `StatusPill` (§6.4) so the dot/pill/label stay in lockstep; labels are uppercase (`RUNNING`).

**Which role a call site uses.** Anything a user reads — a label, a count, a message, a letter mark — takes the `-text` role: `text-status-running-text`, `text-status-waiting-text`, and `text-destructive-text` for `error`. The saturated `status-*` values stay on dots, bars, borders and stand-alone icons (`bg-status-running`, `<Check className="text-status-running" />`). The one case that needs no judgement is enforced by lint: a class string that pairs a soft fill with the same family's saturated color as text (`bg-status-running-bg text-status-running`, `bg-destructive/10 text-destructive`, `bg-primary-soft text-primary`) is rejected — see the [enforced-rules table](./develop.md#enforced-rules-mechanically-checked).

### 3.6 Agent palette (16 identity colors)

Sixteen fixed hues give concurrent agents distinct, stable identities. Light uses **600–700** shades; dark keeps the **300–400** hues but darkens each until the white glyph holds (≥4.6), so avatars read as blocks without glaring on the dark shell. `agent-3` (sky) and `agent-6` (cyan) are tuned to avoid clashing with `status-running` green and the steel `primary`.

| Token | Light | Dark | | Token | Light | Dark |
| --- | --- | --- | --- | --- | --- | --- |
| `agent-1` | `#2964e7` | `#1972e0` | | `agent-9` | `#4f46e5` | `#5866ec` |
| `agent-2` | `#7d3ee9` | `#7e59ed` | | `agent-10` | `#c15015` | `#ae6213` |
| `agent-3` | `#147cb3` | `#137dad` | | `agent-11` | `#0e825f` | `#1d845f` |
| `agent-4` | `#e11d48` | `#e51937` | | `agent-12` | `#0e8076` | `#1b8376` |
| `agent-5` | `#ab6413` | `#946f10` | | `agent-13` | `#d92474` | `#de1980` |
| `agent-6` | `#117d97` | `#108091` | | `agent-14` | `#996d11` | `#88740f` |
| `agent-7` | `#c026d3` | `#c419de` | | `agent-15` | `#64748b` | `#617693` |
| `agent-8` | `#507e0e` | `#568110` | | `agent-16` | `#9334e9` | `#9a4aeb` |

> The initial glyph sitting **on** an agent fill uses `agent-foreground` (`#ffffff`, theme-invariant — the letter is white on all sixteen hues in both themes). Use `text-agent-foreground`, not a literal `text-white`.

**How to apply a color.** The source of truth is the agent's `agentColor` token (e.g. `"agent-7"`), assigned by the backend — there is **no client-side hashing**. Map it through the helpers, never by hand:

- Tailwind classes (the common path) — [`components/agentre/types.ts`](../frontend/src/components/agentre/types.ts): `agentColorClassNames[color]` → `bg-agent-7`; `agentIconColorClassNames[color]` / `agentIconColorClassName(token, fallback)` → `text-agent-7`, **for an icon only**. The extra member `"neutral"` maps to `bg-agent-neutral` / `text-foreground`.
- A raw CSS color (for inline `style`) — [`components/agentre/session-avatar.ts`](../frontend/src/components/agentre/session-avatar.ts): `tokenToCssColor(token)` → `var(--agent-7)` or `null` for an invalid token; `avatarFromMeta(meta)` → `{ letter, color }`, falling back to `#94a3b8` (slate) when meta is missing.

Default fallback color is `agent-1`. `agentColorOrder` is the canonical 1→16 sequence for round-robin assignment.

**Identity color goes on graphics, never on text.** The sixteen hues are tuned as a fill under a white glyph; as text they miss 4.5 — about 3.5 on `card` in dark (all sixteen), and twelve of them on `secondary` in light. A block (`AgentAvatar`) or an icon (the folder beside a project path, the crown on a lead badge) may carry the color, because a graphic only needs 3:1; the words next to it use a neutral text token. `text-agent-N` may be written in exactly one file — the class table in `components/agentre/types.ts` — and is a lint error everywhere else (see the [enforced-rules table](./develop.md#enforced-rules-mechanically-checked)). An accent that is not about *whose* thing it is (a banner, a link) takes the brand color: `text-primary-text` on `primary-soft`.

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

The shared issue-tone palette in [`board/tones.ts`](../frontend/packages/agentre-ui/src/board/tones.ts) uses "soft fill + a text color readable on that fill". Most tones borrow an existing semantic family. Two hues have no semantic home, so they get their own pair here.

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `tone-blue-bg` | `#e9effd` | `#242d3a` | Soft blue chip fill (`auth`) |
| `tone-blue-text` | `#1d4ed8` | `#60a5fa` | Text on `tone-blue-bg` |
| `tone-violet-bg` | `#f2ebfd` | `#2b2b3a` | Soft violet chip fill (`ui`) |
| `tone-violet-text` | `#6d28d9` | `#a78bfa` | Text on `tone-violet-bg` |

**Do not reach into the agent palette for this.** The `--agent-*` hues are *identity* colors built for `bg-agent-N` plus a white glyph (§3.6), not text colors for soft chips. The issue-tone contract and token ownership are guarded in the shared package by [`board/__tests__/tones.test.ts`](../frontend/packages/agentre-ui/src/board/__tests__/tones.test.ts).

### 3.7 Sidebar

Row-state and icon colors for the lists and the icon rail that sit on the frame. The lists' own background is `chrome` (§3.1), not `sidebar`.

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `sidebar` | `#f4f4f5` | `#131314` | The board's column track (`issue-board.tsx`). Not the left lists — they sit on `chrome` |
| `sidebar-foreground` | `#18181b` | `#e8e8e9` | Sidebar text |
| `sidebar-accent` | `#f4f4f5` | `#292a2e` | Hover/selected background |
| `sidebar-active-bg` | `#ffffff` | `#292a2e` | The resting fill of the expanded chevron in the desktop session list's agent group header (`agent-list.tsx`). **Not a row hover on the frame:** on `chrome` it is the same byte as `raised` in light (1.00) and 1.03 from it in dark, so a hovered row reads as the selected one. Rows in the frame's lists hover to `field-fill-hover`; a *selected* row takes `raised` (§3.1). **Not a hover inside a popover either:** it is the same byte as `popover` in both themes (1.00); a popover row hovers to `accent` (the "View all" sessions popover, guarded by [`host-row-hover.test.tsx`](../frontend/src/components/agentre/__tests__/host-row-hover.test.tsx)), and the current entry of a popover menu is `DropdownMenuItem current` (§9 *Selected / current*) |
| `sidebar-border` | `#e4e4e7` | `#2d2e31` | Not painted: sidebars have no edge, and `border-sidebar-border` is a lint error (§3.4) |
| `sidebar-icon` | `#71717a` | `#8a8d94` | Rail icon (resting) |
| `sidebar-icon-active` | `#3b6896` | `#8eb6dc` | Rail icon (active) |

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

Don't restyle scrollbars per-container; the shared rules in [`base.css`](../frontend/packages/agentre-ui/styles/base.css) cover both the Firefox `scrollbar-*` properties and the WebKit pseudo-elements. To hide one entirely (e.g. a horizontal strip), add `.scrollbar-none`.

### 3.11 Destructive

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `destructive` | `#dc2626` | `#f87171` | Dangerous / delete / error actions |
| `destructive-foreground` | `#ffffff` | `#fafafa` | Text on solid destructive |
| `destructive-soft` | `#fef2f2` | `#2a1414` | Soft red wash — error cards, error toasts, the `error` status pill |
| `destructive-text` | `#b91c1c` | `#f87171` | **Red rendered as text** on `destructive-soft` / a card. The same fill-vs-text split as `status-*-text`: `destructive` on its own wash is only 4.41 in light. Keep `destructive` for fills, dots and icon marks; reach for this one whenever the red *is* the text. Dark already clears the bar on the wash (6.28), so it reuses the fill value |

### 3.11a Code / console surface

Monospace **console output** surfaces (hook stdout/stderr, local-command output) — theme-adaptive. The transcript `CodeBlock` container is different: a `TranscriptCard` on the default `field-fill`, with no outer edge and no line under its header row.

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `code-surface` | `#f4f4f5` | `#141516` | Console/output box fill (`bg-code-surface`) |
| `code-foreground` | `#3f3f46` | `#e8e8e9` | Primary monospace text on `code-surface` |
| `code-muted-foreground` | `#65656d` | `#9da1a8` | De-emphasized monospace text (stdout) |

### 3.12 Elevation (surfaces & shadows)

Depth is a **surface step**, never a line (Principle 5). Pick the surface that matches the layer:

| Level | Surface token | Shadow | Use |
| --- | --- | --- | --- |
| **Frame** | `chrome` | none | Title bar, icon rail, left lists, tab strip, chat right panel, status bar, settings nav column |
| **Content sheet** | `sheet` (`content-sheet`) | light `--sheet-shadow`, dark none | The workspace of every page, the settings dialog's content area, the active chat tab |
| **Selected on the frame** | `raised` | none (the icon-rail item adds `shadow-xs`) | Selected session row, selected org index row and department header, current icon-rail item, Hooks selected row, current outline entry |
| **Fill — field tier** | `field-fill` (5%, hover 7%) | none | Fields; cards, code blocks, settings groups, transcript cards |
| **Fill — button tier** | `button-fill` (8%, hover 12%) | none | `outline` / `secondary` buttons, small filled controls, the settings nav's current item |
| **Floating** | `popover` | `shadow-md` (`Select`, `DropdownMenu`: `shadow-overlay`) | Anchored floating layers — `DropdownMenu`, `Popover`, `HoverCard`, `Select`, `Tooltip`, the background-tasks preview card. No border |
| **Overlay** | `card` (settings dialog: `sheet`) | `shadow-overlay` | Detached overlays that own the screen — `Dialog`, `DialogShell`, the settings dialog. No border. The shadow is the `--overlay-shadow` token, a plain drop with **no 1px ring** (light `0 24px 64px -12px` at 18% black, dark the same at 60%); the backdrop is `--overlay-scrim` (`bg-scrim`); the settings dialog's lighter, neutral backdrop is `--settings-scrim` (`bg-settings-scrim`, light 32% black, dark 55% black) |

- **No hairline rings.** A 1px ring in a shadow is a structural line by another name; `--overlay-shadow` has none, guarded by the `浮层阴影不带描边环` suite of [`tokens.test.ts`](../frontend/packages/agentre-ui/src/tokens.test.ts). On dark surfaces, where a black shadow barely renders, floating layers stand on the surface step alone — don't reach past `shadow-overlay`.
- **The one shadow token is `--overlay-shadow` → `shadow-overlay`.** Root token is `--overlay-*` while the utility alias is `--shadow-*` on purpose: Tailwind v4's shadow namespace *is* `--shadow-*`, so a same-named root token would make the `@theme` mapping self-referential. Same reason for `--overlay-scrim` → `--color-scrim`. Anything shallower (`shadow-xs` / `shadow-md`) stays a plain Tailwind utility — don't invent more shadow tokens.
- Pair elevation with the matching radius (§5): raised → `rounded-lg`, overlay → `rounded-xl`.

---

## 4. Theming

**Mechanism:** the theme switches by adding/removing `.dark` on `document.documentElement` (`@custom-variant dark (&:is(.dark *))` is what makes the `dark:` variant work). The toggle is `applyDocumentTheme(theme)` in the shared [`theme-context.tsx`](../frontend/packages/agentre-ui/src/theme/theme-context.tsx), which also sets `data-theme` and `style.colorScheme` for redundancy. Every token is defined under both `:root` and `.dark`, so toggling the class re-skins the whole app — no per-component color changes needed.

**Preference & API.** Theme state is provided by the shared package's `ThemeProvider` and consumed with `useTheme`; the desktop host wraps the app with the provider and passes the resulting values to routed pages through the `Outlet` context. The model lives in the shared [`theme-context.tsx`](../frontend/packages/agentre-ui/src/theme/theme-context.tsx):

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
<div className="bg-sheet text-foreground">…</div>
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

**System-font-only, zero webfonts** — declared as two tokens in the `@theme inline` block of the shared [`tokens.css`](../frontend/packages/agentre-ui/styles/tokens.css). A desktop app must work offline and pays for every byte, so the type system is the platform's own fonts. Both stacks pin an **explicit CJK fallback** (`PingFang SC` / `Microsoft YaHei` / `Noto Sans SC`) because agentre is Chinese-first, and the sans stack pins emoji families so status badges don't drop emoji.

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

The shadcn primitives live in [`frontend/packages/agentre-ui/src/ui/`](../frontend/packages/agentre-ui/src/ui/) and are imported from `"@agentre-hub/agentre-ui"` — `new-york` style, CSS variables enabled, no class prefix ([`components.json`](../frontend/components.json)). Icons are `lucide-react` (plus `@iconify/react` for the agent icon registry); class merging is `cn()` ([`frontend/packages/agentre-ui/src/lib/utils.ts`](../frontend/packages/agentre-ui/src/lib/utils.ts)); variants are CVA — the conventions you'll see in every primitive's source. The **enforced** shadcn-only / icon / i18n rules live in [`frontend.md`](./frontend.md), not repeated here. This section is "what exists and how to choose."

### 6.1 Primitives — present vs. absent

The palette is pruned to what's actually imported. **Present** in [`packages/agentre-ui/src/ui/`](../frontend/packages/agentre-ui/src/ui/):

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
| `toggle.tsx` | Pressed-state toggle |
| `segmented-control.tsx` | `SegmentedControl` / `SegmentedControlItem` — the tab-like switch (`role="tablist"` / `role="tab"`, arrow-key navigation). Use it wherever an underline tab strip would have gone: no track fill, the current item `bg-button-fill` (8% of `foreground`), the others transparent until hovered (`bg-field-fill`) |
| `skeleton.tsx` | Placeholder block while content loads |

**Absent** (commonly expected, but *not* in the codebase — don't import or assume them): `card`, `tabs` (use `SegmentedControl`; no underline tabs), `sheet`, `progress`, `accordion`, `collapsible`, `sonner` (the `Toaster` is imported straight from `sonner`), `command` (there's a custom command palette), `form` (no form library — see §9).

> **No `card.tsx`.** A "card" is the inline convention `rounded-lg bg-field-fill p-4` — a fill, never an outline, and no line between its header and body; a group nested inside a card is separated by spacing, not by another card. Compose it directly; don't add a card primitive without cause. **No `progress`** — see §9 for the real loading conventions. Need a missing primitive back? Re-add it from shadcn rather than hand-rolling.

### 6.2 Button variants / sizes

Source: [`button.tsx`](../frontend/packages/agentre-ui/src/ui/button.tsx).

- **variant:** `default` (solid `bg-primary`), `destructive`, `outline`, `secondary`, `ghost`, `link`
- **size:** `default` (`h-9`), `xs` (`h-6`), `sm` (`h-8`), `lg` (`h-10`), `icon` (`size-9`), `icon-xs` (`size-6`), `icon-sm` (`size-8`), `icon-lg` (`size-10`)

`outline` and `secondary` look the same: no edge, `bg-button-fill text-secondary-foreground hover:bg-button-fill-hover` — 8% of `foreground`, 12% on hover, **relative to the surface under it** rather than the `secondary` token, so it never lands on an identical surface and disappears (the old `bg-secondary` was 1.00 on a dark popover). `default`, `ghost` and `destructive` are unchanged. A hand-written outline-style button takes the same fill, and a small control that used to be recognised only by its outline (an icon tile, a small pill button) gets a fill rather than degrading to bare text. The chat header's actions (background tasks, stop, right-panel toggle, more) are 32px icon-only buttons (`size="icon-sm"`), transparent until hovered, each with an accessible name and a tooltip.

A bare `<svg>` child auto-sizes to `size-4` (`size-3` at `xs`/`icon-xs`). The `default` variant is `bg-primary text-primary-foreground` — `primary` *is* the solid fill here (unlike a separate "primary-background"); reserve `text-primary`/`border-primary` for accent semantics.

```tsx
import { Button } from "@agentre-hub/agentre-ui";
import { Plus } from "lucide-react";

<Button>Create</Button>                                   {/* primary action */}
<Button variant="outline">Cancel</Button>                 {/* secondary action */}
<Button variant="destructive">Delete</Button>             {/* dangerous action */}
<Button variant="ghost" size="icon-sm"><Plus /></Button>  {/* icon button */}
```

### 6.3 Badge variants

Source: [`badge.tsx`](../frontend/packages/agentre-ui/src/ui/badge.tsx). Variants: `default` (`bg-primary`), `secondary`, `destructive`, `outline`, `ghost`, `link`. Badges are `rounded-full`, size-fixed (no size prop), with an auto `size-3` leading svg. For **agent run state**, prefer `StatusPill` (§6.4) over a hand-styled badge.

### 6.4 Agent & status primitives

Project blocks in [`components/agentre/primitives.tsx`](../frontend/src/components/agentre/primitives.tsx) — reuse these for any agent/status surface (Principle 1):

| Component | Use |
| --- | --- |
| `AgentAvatar` | Agent identity avatar. Sizes `xs`/`sm`/`md`/`lg` (`size-3.5`/`6`/`8`/`10`); `xs` is the sub-20px tier and draws a single initial. Renders, in priority: a custom image (`avatarDataUrl`), a registry icon (`avatarIcon`), else initials — on the `agentColorClassNames[color]` fill with white text. Defaults: `color="agent-1"`, `size="md"`. |
| `StatusDot` | Colored run-state dot. Sizes `xs`/`sm`/`md` (`size-1.5`/`2`/`2.5`); `aria-label` = `"<status> status"`. |
| `StatusPill` | Dot + uppercase label pill — `font-mono text-2xs`, `rounded-sm`, on the status' tinted bg. The canonical "agent is RUNNING/WAITING/…" chip. |
| `SidebarButton` | Icon rail button — `ghost`/`icon`, `size-10 rounded-lg`, `sidebar-icon` color; active = `bg-raised text-sidebar-icon-active shadow-xs`; ships its own hover/focus tooltip (300ms hover delay) and `aria-current`. |
| `DeviceTag` | Local/remote device chip (online/offline). |

> Other high-reuse blocks worth knowing before building. In the shared package: `code-block` (highlighted code + copy), `markdown-text` (agent markdown renderer) and `thinking-block` under `transcript/`, `resizable-sidebar` (drag-resized, persisted sidebar shell, §7) under `ui/`, `app-dialog` (generic dialog host) under `engine/`. In [`components/agentre/`](../frontend/src/components/agentre/): `compact-history-fold` (collapsible transcript region) and `icon-picker`. Search both before hand-rolling.

### 6.5 Toasts & notifications

Two distinct systems — use the right one:

- **Transient toasts → Sonner.** `<Toaster position="bottom-right" richColors theme={effectiveTheme} />` is mounted once in `AppLayout` ([`App.tsx`](../frontend/src/App.tsx)). Business code imports `toast` **directly from `sonner`** (`toast.success/error(title, { description, duration })`) — there is no `notify` wrapper. The toast colors are bound to design tokens in the shared [`toast.css`](../frontend/packages/agentre-ui/styles/toast.css) (`[data-sonner-toaster][data-rich-colors="true"]`): success→`status-running-bg`, error→`destructive-soft`, warning→`status-waiting-bg`, info→`primary-soft`; neutral `foreground` text, saturated icon. See [`lib/clipboard-toast.ts`](../frontend/packages/agentre-ui/src/lib/clipboard-toast.ts) for the canonical call.
- **Agent turn-completion → the notification viewport.** `<NotificationToastViewport />` (custom, backed by a Zustand store [`stores/notification-toast-store.ts`](../frontend/src/stores/notification-toast-store.ts)) surfaces turn done / error / awaiting-approval events — up to 5 at once. Use it for *session lifecycle* signals, not generic feedback.

### 6.6 Selection guidance

- **Confirmation:** dangerous / irreversible → a `Dialog` confirm (e.g. `delete-project-dialog`, `quit-confirm-dialog`); state the blast radius in the copy. For easily reversible actions, prefer acting immediately + a Sonner toast over a blocking dialog — fewer interruptions.
- **Transient panels:** anchored small layer → `Popover` / `DropdownMenu` / `HoverCard`; right-click affordance → `ContextMenu`. (No `Sheet` primitive — a side drawer like `project-settings-drawer` is composed directly.)
- **Cards:** inline `rounded-lg bg-field-fill` (§6.1), not a primitive.
- **Tab-like switching:** `SegmentedControl` (§6.1), not an underline tab strip.
- **Feedback:** transient → Sonner toast; session lifecycle → notification viewport; persistent / in-page → §9 state patterns.

---

## 7. Layout & desktop shell

### The one frame

Every screen renders inside `AppLayout` ([`App.tsx`](../frontend/src/App.tsx)). Top to bottom:

The app root, and every band of the frame, paints `bg-chrome`; no line separates any two of them (§3.4).

1. **Title bar** (`AppTopBar`, [`chrome.tsx`](../frontend/src/components/agentre/chrome.tsx)) — `h-11`, `bg-chrome`, `.wails-drag`; holds the app name/breadcrumb. Platform-aware: macOS reserves the 68px native traffic-light inset; Windows renders `WindowsWindowControls`.
2. **Icon rail** — `<aside>` `w-14`, `bg-chrome`, a column of `SidebarButton`s (Chat / Issues / Org / Hooks, from `lib/app-routing.ts`), with the theme toggle + Settings pinned to the bottom via `mt-auto`. The current item is `bg-raised`; the Settings gear shows as current while the settings dialog is open.
3. **Left list** *(per page)* — optional `ResizableSidebar` (session list, org index column, Hooks list), `surface="chrome"` by default, no edge; the selected row is `bg-raised`. Its drag handle draws nothing at rest and a brand bar on hover / drag; width persisted.
4. **Workspace** — the routed page (`Outlet`), or the chat **tab strip** (`h-[38px]`, `bg-chrome`, drag-reorderable, `.scrollbar-none`) + `ChatPanelHost`, toggled via `display:none` by `data-page-has-chat`. Each page's `<main>` is a **`content-sheet`** (§3.1): one rounded sheet with an 8px gap to the frame on the right and bottom. In the tab strip no line runs between tabs; the active tab is `bg-sheet` with the sheet's 12px top radius (`rounded-t-xl`) and 8px inverse corners at its foot (`tab-flare`, `tokens.css`), so it flows into the sheet below it (no brand bar); the strip is inset `pl-5` (12px sheet radius + 8px flare) so the first tab's flare lands on the sheet's straight top edge, not its rounded corner. Inactive tabs hover to an inset `button-fill` pill (`tab-hover-pill`) that never covers a neighbouring flare. On the chat page the title, transcript and composer share the sheet with no line under the title; the composer is borderless, `rounded-xl bg-field-fill`, and turns `bg-sheet` + focus ring when focused.
5. **Right panel** *(chat page)* — the outline / changes / directory panel (`ChatContextSidebar`) sits **on the frame**, to the right of the sheet and outside it, from the tab strip's lower edge to the bottom. It keeps its toggle, drag-resize and persisted width (`agentre.sidebarWidth.chat-context`) and its text tabs with counts; the current outline entry is `bg-raised` (no brand bar), and an entry's edit count sits at the end of its time row (`✎ N`).
6. **Status bar** (`AppStatusBar`) — `h-7`, `bg-chrome`; agent summary, connection status, version.

Swap the content in the `Outlet`/panel — keep the frame. Exactly one `main` landmark is on screen: the page under any dialog.

### Settings dialog

Settings is not a page route: `SettingsDialog` ([`settings/page.tsx`](../frontend/src/components/agentre/settings/page.tsx)) floats over whatever page is open, driven by [`stores/settings-dialog-store.ts`](../frontend/src/stores/settings-dialog-store.ts) (`openSettings(page?)` / `closeSettings()` / `toggleSettings()`), and the page beneath — chat panel, scroll position, composer draft — stays mounted.

- **Opening.** The gear, ⌘, / Ctrl+,, the command palette, and every deep link open the dialog; a link with a sub-page lands on it, one without lands on the sub-page last opened in this run (first time: Appearance). Coming from another dialog closes that one first. While open, a link with a sub-page switches to it and ⌘, closes the dialog.
- **Look.** Centered with roughly 32px top/bottom and 56px left/right of window around it (`h-[calc(100dvh-4rem)] w-[calc(100vw-7rem)]`), at most 1180px wide, `rounded-2xl`, `bg-sheet` + `shadow-overlay`, no border, over a `bg-settings-scrim` backdrop (light 32% black, dark 55% black — lighter than the form dialogs' `bg-scrim`, because the page underneath only has to recede); 200ms fade, none under reduced motion. The left column is the nav (`w-[236px] bg-chrome`, fixed width, group titles and icons; the current item `bg-button-fill`); the right is the content area on `sheet`, scrolling on its own, with extra right padding so a page title's buttons clear the close button. The close button (icon-only, named "Close") sits top-right.
- **Closing.** Esc, the close button, a click on the backdrop and ⌘, all close it and return focus to where it was. Any navigation also closes it — opening a remote session from inside settings, a page shortcut such as ⌘B, a page picked in the command palette — keyed on the router's `location.key`, so navigating to the path already underneath (opening a session while settings floats over chat) counts too. A dialog opened from inside settings stacks above it, and Esc / backdrop close only the topmost layer; Esc while recording a shortcut only cancels the recording.
- **Window.** A transparent `h-11` strip over the title bar keeps the window draggable while the dialog is open. The content area is not a `main` landmark. `/settings` is not a route and is never written to `agentre.lastPath`; an old stored `/settings` resolves as an unknown path and lands on chat.

### Wails window chrome

- **Drag regions:** the title bar is `.wails-drag` (`--wails-draggable: drag`); interactive children opt out with `.wails-no-drag`. Double-clicking the bar calls `WindowToggleMaximise()` (unless the target is `no-drag`). Window controls call `WindowMinimise()` / `WindowToggleMaximise()` / `Quit()`.
- **Window size** is persisted to `localStorage["agentre.windowSize"]`, clamped to min `860×640`. Don't assume a viewport narrower than the minimum.

### `user-select` discipline

The body defaults to `user-select: none` (this is an app chrome, not a document). Text selection is **opted into**: `input` / `textarea` / `[contenteditable]` and any region marked `data-selectable-text="true"` get `user-select: text` (and their nested buttons/icons opt back out). When you build a panel whose text the user should be able to copy (transcripts, code, errors), mark it `data-selectable-text="true"`; for a control whose label should be copyable, use `data-copyable-control-text="true"`.

### Auto-hiding scrollbars

Scrollbars are invisible until you scroll. `useAutoHideScrollbars` (the shared [`use-auto-hide-scrollbars.ts`](../frontend/packages/agentre-ui/src/hooks/use-auto-hide-scrollbars.ts)) sets the `--sb-thumb` CSS variable to a visible color on scroll and clears it after ~900ms idle. This is a **CSS-variable** mechanism on purpose: WKWebView doesn't repaint `::-webkit-scrollbar` on class/selector changes (WebKit bug #104412), but it does on custom-property value changes. So: **don't** restyle scrollbars with classes per container; rely on the global rules. To fully hide one (a horizontal strip like the tab list), add `.scrollbar-none`.

### No mobile — desktop only

There is **no `useIsMobile`, no `MOBILE_BREAKPOINT`, and no mobile re-shell.** Responsive utility adaptations (`sm:` / `md:` / `lg:`) do exist across the desktop chrome and pages to manage a resizable window; they rearrange or hide secondary desktop UI, not create a phone layout. Design for the desktop minimum and resizing behavior; don't add a separate mobile branch.

### Long lists

The chat transcript can hold thousands of rows and is windowed with **`@tanstack/react-virtual`** ([`chat/transcript.tsx`](../frontend/src/components/agentre/chat/transcript.tsx)): dynamic per-row size estimation, overscan, `anchorTo: "end"` stick-to-bottom, and `measureElement` for real heights (see [`transcript-rows.ts`](../frontend/packages/agentre-ui/src/transcript/transcript-rows.ts) + [`transcript-row-view.tsx`](../frontend/packages/agentre-ui/src/transcript/transcript-row-view.tsx)). Other lists (issues, org chart, settings) are bounded and render plainly — don't add virtualization unprompted, but **do** virtualize any new unbounded list rather than mounting every row.

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

**Sources:** `tw-animate-css` (`@import` in the host [`globals.css`](../frontend/src/styles/globals.css) — provides `animate-in/out`, `fade-*`, `zoom-*`, `slide-*`) + Radix `data-state` + one custom keyframe (`typing-dot`). **No Framer Motion** — all motion is CSS.

### How to add motion that stays friendly

- **Fast and light:** micro-interactions `150ms` `ease-out`; small layout entrances `200ms` (`animate-in fade-in slide-in-from-bottom-1 duration-200 ease-out`).
- **Hover/focus via CSS pseudo-classes, not React state** — a Constraint 1 rule.
- **Enter/leave via Radix `data-state`** — `data-[state=open]:animate-in … data-[state=closed]:animate-out` with `fade-*`/`zoom-in-95` + `zoom-out-95`/`slide-*` (see `dialog.tsx`, `dropdown-menu.tsx`, `hover-card.tsx`). Don't hand-roll show/hide with `setTimeout`.
- **Prefer `transition-colors`/`transition-transform` over `transition-all`** — animate only what should move. Common: chevron rotation `transition-transform duration-150`, collapse via `transition-[grid-template-rows] duration-150`.
- **Spinners:** `animate-spin` on a `lucide` spinner (`Loader2` / `RefreshCw` / `LoaderCircle`), sized to context (`size-3.5`/`size-4` inline), usually tinted to the active status.
- **Honor reduced motion explicitly.** Agentre does **not** rely on a global `prefers-reduced-motion` reset — instead each animation carries a `motion-reduce:animate-none` / `motion-reduce:transition-none` (or `motion-safe:`) modifier. **Always add one** when you introduce motion; it's how this codebase stays reduced-motion-safe. A Radix enter/leave cannot be calmed with `motion-reduce:animate-none`: `data-[state=open]:animate-in` compiles to a class plus an attribute selector, outranks the single-class reduced-motion rule and keeps playing. Put `motion-safe:` in front of every `data-[state=…]:animate-*` / `fade-*` utility instead (see `SettingsDialog` in [`settings/page.tsx`](../frontend/src/components/agentre/settings/page.tsx)); an unconditional `animate-*` such as `animate-pulse` is still fine with `motion-reduce:animate-none`.

### Available animations

| utility / pattern | Use |
| --- | --- |
| `animate-typing-dot` (`--animate-typing-dot`) | The chat typing/compacting indicator — three dots with staggered `[animation-delay:…]` (see `TypingIndicator` in [`transcript-row-view.tsx`](../frontend/packages/agentre-ui/src/transcript/transcript-row-view.tsx)). Keyframe `typing-dot` defined in the shared [`tokens.css`](../frontend/packages/agentre-ui/styles/tokens.css). |
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

Every async flow covers loading / empty / error / success consistently. **Important:** unlike some shadcn apps, agentre has **no shared `StateScreen` / `EmptyState` / `LoadingState` / `Progress` components yet** (only a `Skeleton` primitive) — these states are currently composed ad hoc. Follow the conventions below, and **when a state block repeats across two or more pages, extract a shared component** rather than copy-pasting (Principle 6).

| State | Convention today |
| --- | --- |
| **Loading** | A centered `Loader2` (`animate-spin`, `text-muted-foreground`) for whole regions, or an inline `Loader2 size-3.5 animate-spin` inside a disabled button for single actions. For lightweight "first load" copy, centered `text-xs text-muted-foreground`. For placeholder rows, the `Skeleton` primitive (e.g. [`exec-target-list.tsx`](../frontend/src/components/agentre/org/exec-target-list.tsx)). |
| **Empty** | Centered icon (`Inbox` / `Sparkles` in `decorative-foreground`/`primary-soft`) + `text-sm font-semibold` title + `text-xs text-muted-foreground` description + a primary CTA. See `ProvidersEmptyState` ([`llm-providers.tsx`](../frontend/packages/agentre-ui/src/engine/llm-providers.tsx)). |
| **Error** | `ErrorCard` ([`transcript-row-view.tsx`](../frontend/packages/agentre-ui/src/transcript/transcript-row-view.tsx)): `bg-destructive-soft` with no edge, `TriangleAlert` icon in `text-status-error`, the message in `text-destructive-text`, and an optional outline retry button. For page-level failures, centered `text-destructive-text` copy. |
| **Success** | Transient → a Sonner `toast.success` (§6.5); a completed agent turn → the notification viewport. |
| **In-progress** | No general-purpose `Progress` primitive in the shared package. Agent background-task progress has a dedicated `TaskProgressBar` ([`task-progress/`](../frontend/src/components/agentre/task-progress/)) — a real bar + expandable task list with a `LoaderCircle` spinner tinted `text-status-waiting`. For other waits, a status-tinted spinner + readable copy. |

### Forms & validation

Forms are plain `useState` + controlled shadcn components — **no react-hook-form / zod**, no `form.tsx`. Keep new forms on this pattern unless asked. Guidance:

- **Validate late, forgive early.** Don't error while a field is being filled; validate on blur/submit, then switch to live revalidation so the message clears the instant it's fixed.
- **Error sits with the field:** a short `text-destructive-text text-xs` line under the input, mark the control (`aria-invalid`). For a failed *submit*, raise a Sonner error toast (there is no `Alert`-banner convention for form-level errors).
- **Don't lose input on failure** — a failed save keeps every field as-is.
- **Submit in flight:** disable the button + inline `Loader2`.

### Writing & microcopy

- **Through i18n always** (`t(...)`, zh-CN + en) — Constraint, see [`frontend.md`](./frontend.md). Don't hardcode Chinese.
- **Buttons are verbs** naming the action ("Create", "Save", "Delete"), with an in-flight progress label ("Saving…").
- **Errors are specific and actionable:** what failed + why + next step, not "Something went wrong." Put raw detail in a mono region, not the headline.
- **Sentence case** for buttons/titles/labels; product names keep their casing. Status labels are the exception — uppercase (`RUNNING`).

### Interactive states

- **Hover/focus** via CSS pseudo-classes, never React state (§1, §8).
- **Hover is always the feedback color, never a resting surface.** `hover:bg-accent`; a lighter touch is `hover:bg-accent/60 hover:on-accent`; on the window chrome bands it is `hover:bg-rail-accent`. `muted` and `secondary` each equal some other surface (dark `muted` ≡ `card`, `secondary` ≡ `popover`; light `secondary` ≡ `sidebar`), so as a hover fill they land at 1.00 there — `hover:bg-muted` / `hover:bg-secondary` are lint errors (see the [enforced-rules table](./develop.md#enforced-rules-mechanically-checked)). The fills have their own hover step instead: a field `hover:bg-field-fill-hover` (the title-bar command-palette trigger is such a field), a filled button `hover:bg-button-fill-hover`, an inactive chat tab on the `chrome` strip `hover:bg-button-fill`, an unselected row in the frame's lists `hover:bg-field-fill-hover` (not `button-fill`: in dark it lands 1.07 from the selected `raised`). The file-preview tab strip is `bg-muted`, so its tabs hover to `accent` like everything else.
- **Something that is not interactive gets no hover at all** — don't cancel a primitive's hover by repeating the resting fill under `hover:` (that is the banned spelling above). A `TableRow` inside `TableHeader` already carries no hover / expanded / selected feedback, so a header row only needs its resting fill (`<TableRow className="bg-secondary">`); guarded by [`table.test.tsx`](../frontend/packages/agentre-ui/src/ui/table.test.tsx).
- **Disabled:** the shadcn primitives already apply `disabled:opacity-50 disabled:pointer-events-none` — reuse them; give a non-obvious disable a reason nearby.
- **Fields:** at rest `bg-field-fill`, no edge; on hover `bg-field-fill-hover`; when focused or open the fill returns to `bg-sheet` and the only thing that appears is the focus ring (§10) — no edge turns solid, no halo. `aria-invalid` shows a destructive ring (`ring-2 ring-destructive`). In-place editable text (a board task's title and description) is `Input` / `Textarea` with `variant="inline"`: no fill, only the focus ring.
- **Selected / current:** on the frame a selected item is `bg-raised`; in a segmented control the current item is `bg-button-fill`; in a popover menu the current entry (the open tab in the chat-tab and file-preview overflow menus) is `<DropdownMenuItem current>`, which paints `bg-field-fill font-medium` and sets `aria-current` — 1.11 / 1.15 from `popover` and 1.19 / 1.16 from the hover/focus `accent` (light / dark), guarded by [`popover-menu-current.test.tsx`](../frontend/packages/agentre-ui/src/popover-menu-current.test.tsx) and [`host-row-hover.test.tsx`](../frontend/src/components/agentre/__tests__/host-row-hover.test.tsx) (`primary-soft`, the `Select` checked item, is only 1.05 from a dark `popover`); elsewhere persistent selection uses `accent` fills or `primary-text` text. Always paired with a non-color cue (`aria-current`, `aria-selected`) — distinct from transient hover (§10).

The rule: **no silent operations** — after any action the user can see success / failure / in-progress.

---

## 10. Accessibility

Friendly UX includes keyboard, screen-reader, low-vision, and motion-sensitive users. Verify these alongside the both-themes check.

### Contrast

- **Target WCAG AA:** ≥ 4.5:1 for normal text, ≥ 3:1 for large text and meaningful UI/icon edges. `foreground`, the status `*-fg` pairs, and brand `primary-text` pass comfortably. Fields carry no edge: they are recognised by their fill, label, placeholder and position, and muted text or a placeholder on any fill — hovered, or a field on `chrome` — reads at ≥4.5 through `fill-muted-foreground` (§3.1).
- **`muted-foreground` is the floor for readable text** — secondary/descriptive copy lives there; use `foreground` for anything dense or critical. `decorative-foreground` is fainter still and is **not** a text color: it is for glyphs that carry no information (§3.1). Both are guarded per-surface, so "it looks fine on a card" is no longer the test.
- **Terminal output is colored by the program, not by us.** The xterm palettes in [`terminal-theme.ts`](../frontend/packages/agentre-ui/src/terminal/terminal-theme.ts) cannot guarantee what a program does with them — the light theme's bright colors sit at 1.65–2.27 on the terminal background, and programs also emit 256-color and truecolor. Both xterm instances (the interactive panel and the read-only command card) therefore run with `minimumContrastRatio` set to `TERMINAL_MIN_CONTRAST_RATIO` (4.5): xterm leaves a passing foreground alone and pushes a failing one toward black/white until it passes.
- **Never encode meaning in color alone.** The agent palette is *identity*, not status — always pair an agent color with its name/initials, and every status color with its label/icon (`StatusPill` does both).

### Focus visibility

Keyboard focus is one look — a solid outline, drawn inward where a container would clip it — for buttons and fields alike, defined once in the shared [`tokens.css`](../frontend/packages/agentre-ui/styles/tokens.css) and applied under a variant. It is the only line on the page that *appears*:

| Utility | Draws | Use on |
| --- | --- | --- |
| `focus-visible:focus-outline` | A 2px solid `ring` outline with a 2px gap | Anything button-like: buttons, rows, tabs, chips, `Switch`, `Checkbox`, radio items. **Fields too:** `Input` / `Textarea` / `SelectTrigger` (which also return their fill to `bg-sheet`); a shell that wraps a field takes `focus-within:focus-outline` (`SearchInput`, the chat composer) |
| `focus-visible:focus-outline-inset` | The same 2px solid `ring`, drawn **inward** (offset −2px) | A button-like element that sits flush against a clipping container, where an outward ring would be cut off: a tab that fills an `overflow-hidden` tab strip (chat tabs, preview tabs), a row flush with a list's clipped edge (session rows, and the two other full-width rows inside a session group's `overflow-hidden` body — the own-sessions header and the "View all" entry). The ring then sits on the element's own fill, so it is guarded against every row fill it lands on |
| `focus-visible:focus-halo` | The edge turns solid `ring`, plus a 3px halo | Still defined, but no component uses it: the fields are borderless, so there is no edge to turn solid |

The outline is solid on purpose: the hand-assembled `ring-ring/50` it replaces composited to about 2.1:1 and `ring-ring/40` to about 1.8:1, whereas solid `ring` is 4.1–5.8:1 on every surface (guarded by [`tokens.test.ts`](../frontend/packages/agentre-ui/src/tokens.test.ts)). The gap keeps the ring from fusing with a solid primary button, whose fill is the same color.

The inward ring gives up that gap, so it is only for the flush-clipped case, and it has two conditions of its own. It goes on **the element that takes keyboard focus**, which is not always the one that looks like the control: a chat tab's focus stop is the sortable wrapper around the `role="tab"` element (dnd-kit gives the wrapper `role="button"` and `tabIndex={0}`), so the wrapper in [`tab-strip.tsx`](../frontend/src/components/agentre/chat-tabs/tab-strip.tsx) carries the utility. And because the ring lies inside the element's own box, **a positioned child paints over it** — an opaque positioned child hides it completely. Where the focus stop wraps such a child, make the focus stop a stacking context and sink the child below it (`isolate` on the wrapper, `-z-10` on the child, as the chat tab does); the two classes go together, since without `isolate` the child drops behind the surface underneath.

**Native button-like elements get the outline by default.** The base layer in [`base.css`](../frontend/packages/agentre-ui/styles/base.css) draws the same 2px solid `ring` + 2px gap on `:focus-visible` for `button`, `a`, `summary`, `[role="button"]` and `[role="tab"]` (guarded by [`host-stylesheets.test.ts`](../frontend/packages/agentre-ui/src/styles/host-stylesheets.test.ts)), so a hand-written `<button>` with no focus class is already correct. Two cases still need the utility written out: an element that removes the outline (`outline-none` / `outline-hidden`) must put it back with `focus-visible:focus-outline`, and a focusable element the base rule does not match (a `div`/`span` with `tabIndex={0}` and another role) must carry it itself — and be reachable (`tabIndex={0}` + key handlers). Never write `focus-visible:outline-none`: it is emitted after `focus-visible:focus-outline` and erases the ring. Do not assemble a ring from `ring-*` utilities — `ring-ring` in a `ts`/`tsx` file is a lint error (see the [enforced-rules table](./develop.md#enforced-rules-mechanically-checked)) — and don't strip focus styling to "clean up" a layout.

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

- [ ] **Shell:** render inside the existing `AppLayout` frame — a routed `Outlet` page (title bar / rail / status bar are given) whose `<main>` is a `content-sheet`. Add a `ResizableSidebar` (on `chrome`) only if the page needs a list/detail split (§7). Settings content goes into the settings dialog, not a new route.
- [ ] **Color** entirely from tokens (`bg-sheet` / `text-foreground` / `bg-field-fill` / `text-primary` / `bg-primary` …), no literals, verified on both themes (Constraints 1–2, §3–4).
- [ ] **Agent/status surfaces** reuse `AgentAvatar` / `StatusDot` / `StatusPill` and the `agentColorClassNames` / `tokenToCssColor` helpers — never re-derive agent color or re-style status (Principle 1, §3.5–3.6, §6.4).
- [ ] **Components** reuse first — search [`components/agentre/`](../frontend/src/components/agentre/) and [`packages/agentre-ui/src/ui/`](../frontend/packages/agentre-ui/src/ui/) before building; remember there's **no `card`/`tabs`/`sheet`/`progress`** primitive — compose the inline card, use `SegmentedControl` for tab-like switching, and the §9 state patterns; variants via CVA, classes via `cn()`, icons via `lucide-react` (§6).
- [ ] **No structural lines** (§3.4): the header and footer are separated from the body by spacing, not `border-b` / `border-t`; cards are inline `rounded-lg bg-field-fill p-4`; fields and outline buttons are fills. Lint rejects divider colors, `divide-*` and field / button edges.
- [ ] **State:** loading / empty / error / success all covered, never silent (§9); extract a shared state block if it repeats.
- [ ] **Motion** restrained (`150–200ms`, `ease-out`), hover/focus via pseudo-classes, enter/leave via `data-state`, and **a `motion-reduce:` modifier on every animation** (§8).
- [ ] **Depth** uses the surface ladder (§3.12) and the z-index ladder (`z-10`–`z-20` chrome / `z-50` floating, §7) — no borders to fake depth, no magic `z-[…]`.
- [ ] **Long lists** virtualize with `@tanstack/react-virtual` if unbounded (§7).
- [ ] **Accessibility:** AA contrast on both themes; meaning never color-only; custom controls keyboard-reachable with a visible focus ring; `aria-label` on icon buttons; reduced-motion-safe (§10).
- [ ] **Copy** through i18n (zh-CN + en), sentence-case, verbs on buttons, specific errors (§9, [`frontend.md`](./frontend.md)).
- [ ] **`user-select`:** mark copyable text regions `data-selectable-text="true"` (the body defaults to no-select, §7).

Page skeleton (tokens + existing primitives + the routed-page pattern):

```tsx
import { useTranslation } from "react-i18next";
import { Button } from "@agentre-hub/agentre-ui";

export default function ExamplePage() {
  const { t } = useTranslation();
  return (
    // the page is one content sheet on the chrome frame (title bar / rail / status bar come from AppLayout)
    <main className="content-sheet flex min-h-0 min-w-0 flex-1 flex-col text-foreground">
      {/* page header — set apart by spacing, no border-b */}
      <header className="flex h-11 shrink-0 items-center px-4">
        <h1 className="text-sm font-semibold">{t("example.title")}</h1>
      </header>

      {/* scroll region — global auto-hiding scrollbar applies */}
      <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4" data-selectable-text="true">
        <section className="mx-auto w-full max-w-[864px] space-y-3">
          <div className="rounded-lg bg-field-fill p-4">…</div>
        </section>
      </div>

      {/* action bar — no border-t */}
      <footer className="flex shrink-0 justify-end gap-2 px-4 py-3">
        <Button variant="outline">{t("common.cancel")}</Button>
        <Button>{t("common.confirm")}</Button>
      </footer>
    </main>
  );
}
```

---

## 12. Sources & verification

**Implementation source of truth (read/edit these when changing the design):**

- Color / font / motion / scrollbar tokens → [`frontend/packages/agentre-ui/styles/tokens.css`](../frontend/packages/agentre-ui/styles/tokens.css) (imported by the host [`globals.css`](../frontend/src/styles/globals.css)); code highlighting → [`code-highlight.css`](../frontend/packages/agentre-ui/styles/code-highlight.css)
- Theming → the shared [`theme-context.tsx`](../frontend/packages/agentre-ui/src/theme/theme-context.tsx); auto-hide scrollbars → the shared [`use-auto-hide-scrollbars.ts`](../frontend/packages/agentre-ui/src/hooks/use-auto-hide-scrollbars.ts); the app shell (title bar, rail, status bar, window size) → [`frontend/src/App.tsx`](../frontend/src/App.tsx) + [`frontend/src/components/agentre/chrome.tsx`](../frontend/src/components/agentre/chrome.tsx)
- Agent color / status model → [`frontend/src/components/agentre/types.ts`](../frontend/src/components/agentre/types.ts) + [`session-avatar.ts`](../frontend/src/components/agentre/session-avatar.ts); agent/status primitives → [`primitives.tsx`](../frontend/src/components/agentre/primitives.tsx)
- Component primitives → [`frontend/packages/agentre-ui/src/ui/`](../frontend/packages/agentre-ui/src/ui/); shadcn config → [`components.json`](../frontend/components.json); `cn()` → [`frontend/packages/agentre-ui/src/lib/utils.ts`](../frontend/packages/agentre-ui/src/lib/utils.ts)

**Related docs:** UI hard rules (shadcn-only, i18n, lint, commit flow) → [`frontend.md`](./frontend.md); layering / dependency direction / storage → [`architecture.md`](./architecture.md); TDD / SOLID → [`develop.md`](./develop.md); test design → [`testing.md`](./testing.md); doc fact-checking → [`documentation.md`](./documentation.md).

> When editing this doc, follow [`documentation.md`](./documentation.md): stage the intended change, derive `VERIFY_TREE="$(git write-tree)"`, and verify token values, component names, and variants against that proposed tree; enumerate counts and lists rather than trusting memory.
