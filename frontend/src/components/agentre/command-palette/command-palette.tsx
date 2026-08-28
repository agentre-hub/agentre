import * as React from "react";
import { Command as CommandPrimitive } from "cmdk";
import type { TFunction } from "i18next";
import {
  Check,
  ChevronDown,
  Folder,
  FolderMinus,
  Search,
  Sparkles,
  Terminal,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router-dom";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@agentre-hub/agentre-ui";
import { useProjectList, type ProjectFlat } from "@/hooks/use-project-list";
import type { AgentSlim } from "@/hooks/use-chat-agents";
import { cn } from "@/lib/utils";
import { useCommandPaletteStore } from "@/stores/command-palette-store";
import {
  clearLastContext,
  readLastContext,
  writeLastContext,
} from "@/stores/new-chat-context-persistence";
import { useNewChatContextStore } from "@/stores/new-chat-context-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";

import { formatChord } from "../shortcuts/format";
import { CMD_NEW_CHAT_ID } from "../shortcuts/registry";
import { useOptionalShortcutsContext } from "../shortcuts/shortcuts-provider";
import { NotChattableDialog } from "../not-chattable";
import { COMMAND_PREFIX, parseMode, type PaletteMode } from "./mode";
import { chatSessionsSource } from "./sources/chat-sessions-source";
import { navigationSource } from "./sources/navigation-source";
import { newAgentSource } from "./sources/new-agent-source";
import { newChatSource } from "./sources/new-chat-source";
import { newProjectChatSource } from "./sources/new-project-chat-source";
import type { CommandItemBase, CommandSource, OnSelectCtx } from "./types";

// 命令源数组：按 source.modes + source.activeFor(ctx) 在不同模式 / 上下文下过滤。
// 加新源（导航 / 项目 / 动作）只动这一行 + 自己的 modes / activeFor 字段。
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const SOURCES: CommandSource<any>[] = [
  chatSessionsSource,
  navigationSource,
  newChatSource,
  newProjectChatSource,
  newAgentSource,
];

export function CommandPalette(): React.ReactElement {
  const { t } = useTranslation();
  const open = useCommandPaletteStore((s) => s.open);
  const initialQuery = useCommandPaletteStore((s) => s.initialQuery);
  const setOpen = useCommandPaletteStore((s) => s.setOpen);
  const close = useCommandPaletteStore((s) => s.close);
  const navigate = useNavigate();
  const location = useLocation();
  const openSession = useChatTabsStore((s) => s.openSession);
  const openSessionInNewTab = useChatTabsStore((s) => s.openSessionInNewTab);
  const openNewSessionRaw = useChatTabsStore((s) => s.openNewSession);
  const [query, setQuery] = React.useState("");
  const [selectedValue, setSelectedValue] = React.useState("");
  const selectedValueRef = React.useRef(selectedValue);
  const selectionTouchedRef = React.useRef({ scope: "", touched: false });
  const candidatesRef = React.useRef({
    scope: "",
    byPriority: new Map<number, string[]>(),
  });
  const { mode, payload } = parseMode(query);
  // 不可对话分支选中后由面板宿主打开引导弹窗（onSelect 里先 close 面板再设置）。
  const [guidanceAgent, setGuidanceAgent] = React.useState<AgentSlim | null>(
    null,
  );
  // 提到 root 一份：SearchRow 的 Tab 循环 + ContextBar 的下拉共享同一份列表，
  // 避免双倍 ProjectListTree RPC。
  const { projects } = useProjectList();
  // 「新建对话」落在哪个项目里由这一条上下文决定 —— 不再由路由决定：
  // 「项目」已经从一个页面退化成会话索引的一个分组轴，没有「项目路由」可判。
  const projectContext = useNewChatContextStore((s) => s.projectContext);

  // open 翻 true 时：
  //   1) 把 store 的 seed 拷到本地 query，并立刻清掉 store 的 initialQuery
  //      —— "消费 once" 语义，避免 ⌘N → 关 → ⌘P 复读旧 seed 进入命令模式
  //   2) 如果 new-chat-context store 是空（project-page 没注入）、且 localStorage
  //      里有上次手动选过的 context，回放它作为默认值
  React.useEffect(() => {
    if (open) {
      setQuery(initialQuery);
      if (initialQuery !== "") {
        useCommandPaletteStore.setState({ initialQuery: "" });
      }
      const ctxStore = useNewChatContextStore.getState();
      if (!ctxStore.projectContext) {
        const last = readLastContext();
        if (last) {
          ctxStore.setContext({
            projectID: last.projectID,
            projectName: last.projectName,
          });
        }
      }
    } else {
      setQuery("");
    }
    // 故意只依赖 open —— initialQuery 变化时如果面板已开，不要二次重置 query
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const ctx = React.useMemo<OnSelectCtx>(
    () => ({
      navigate,
      close,
      // 命令面板只走自由会话(无 project / workMode)。带项目上下文的新会话由
      // project-page 注册的 newSelectionHandler 直接调用 useChatTabsStore。
      openSession: (sid, opts) =>
        opts?.newTab ? openSessionInNewTab(sid) : openSession(sid),
      openNewSession: (agentId) => openNewSessionRaw(0, agentId, ""),
      openNotChattableDialog: (agent) => setGuidanceAgent(agent),
    }),
    [
      navigate,
      close,
      openSession,
      openSessionInNewTab,
      openNewSessionRaw,
      setGuidanceAgent,
    ],
  );

  const activeSources = SOURCES.filter(
    (s) =>
      s.modes.includes(mode) &&
      (s.activeFor == null ||
        s.activeFor({ hasProjectContext: projectContext != null })),
  );

  React.useEffect(() => {
    selectedValueRef.current = selectedValue;
  }, [selectedValue]);

  const selectionScope = `${open}:${mode}:${location.pathname}:${payload.trim()}`;
  // cmdk registers later source groups first, so command mode owns selection and
  // re-applies the highest-priority source after async groups finish loading.
  // eslint-disable-next-line react-hooks/preserve-manual-memoization
  const sourceSelection = React.useMemo(
    () => ({
      reportFirstSelectable: (sourcePriority: number, itemKeys: string[]) => {
        if (mode !== "command") return;

        if (candidatesRef.current.scope !== selectionScope) {
          candidatesRef.current = {
            scope: selectionScope,
            byPriority: new Map<number, string[]>(),
          };
        }
        candidatesRef.current.byPriority.set(sourcePriority, itemKeys);

        if (selectionTouchedRef.current.scope !== selectionScope) {
          selectionTouchedRef.current = {
            scope: selectionScope,
            touched: false,
          };
        }

        const firstAvailable = Array.from(
          candidatesRef.current.byPriority.entries(),
        )
          .sort(([left], [right]) => left - right)
          .find(([, candidates]) => candidates.length > 0)?.[1][0];
        const selectionUnavailable =
          selectedValueRef.current !== "" &&
          !document.querySelector(
            `[cmdk-item][data-value="${CSS.escape(selectedValueRef.current)}"]:not([aria-disabled="true"])`,
          );
        if (!firstAvailable || firstAvailable === selectedValueRef.current)
          return;
        if (selectionTouchedRef.current.touched && !selectionUnavailable)
          return;

        selectedValueRef.current = firstAvailable;
        setSelectedValue(firstAvailable);
      },
      markTouched: () => {
        selectionTouchedRef.current = { scope: selectionScope, touched: true };
      },
    }),
    // eslint-disable-next-line react-hooks/preserve-manual-memoization
    [mode, selectionScope],
  );

  return (
    <>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          showCloseButton={false}
          className={cn(
            // 设计稿宽 640，垂直在 96px 顶部
            "w-[640px] max-w-[92vw] translate-y-0 top-[96px] grid-rows-[auto_1fr_auto] gap-0 overflow-hidden rounded-xl border border-border bg-popover p-0 text-popover-foreground shadow-[0_8px_16px_rgba(10,10,10,0.15),0_24px_60px_rgba(10,10,10,0.25)]",
          )}
        >
          <DialogTitle className="sr-only">
            {t("commandPalette.title")}
          </DialogTitle>
          <DialogDescription className="sr-only">
            {t("commandPalette.description")}
          </DialogDescription>

          <CommandPrimitive
            key={mode}
            // 关掉 cmdk 内置 filter / sort —— 我们用 source.getScore 自己排
            shouldFilter={false}
            loop
            value={mode === "command" ? selectedValue : undefined}
            onValueChange={mode === "command" ? setSelectedValue : undefined}
            onKeyDownCapture={(event) => {
              if (
                event.key === "ArrowDown" ||
                event.key === "ArrowUp" ||
                event.key === "Home" ||
                event.key === "End" ||
                ((event.key === "n" ||
                  event.key === "j" ||
                  event.key === "p" ||
                  event.key === "k") &&
                  event.ctrlKey)
              ) {
                sourceSelection.markTouched();
              }
            }}
            onPointerMoveCapture={(event) => {
              if ((event.target as Element).closest("[cmdk-item]")) {
                sourceSelection.markTouched();
              }
            }}
            onPointerDownCapture={(event) => {
              if ((event.target as Element).closest("[cmdk-item]")) {
                sourceSelection.markTouched();
              }
            }}
            label={t("commandPalette.title")}
            className="flex h-full flex-col overflow-hidden"
          >
            <SearchRow
              query={query}
              mode={mode}
              payload={payload}
              projects={projects}
              onQueryChange={setQuery}
              onClose={close}
            />
            {mode === "command" ? <ContextBar projects={projects} /> : null}
            <CommandPrimitive.List className="max-h-[60vh] overflow-y-auto px-2 pb-2 pt-1">
              <CommandPrimitive.Empty className="px-4 py-10 text-center text-xs text-muted-foreground">
                {emptyText(mode, payload, t)}
              </CommandPrimitive.Empty>
              {activeSources.map((source, sourcePriority) => (
                <SourceGroup
                  key={source.id}
                  source={source}
                  sourcePriority={sourcePriority}
                  query={payload}
                  ctx={ctx}
                  onFirstSelectableChange={
                    sourceSelection.reportFirstSelectable
                  }
                />
              ))}
            </CommandPrimitive.List>
            <Footer mode={mode} />
          </CommandPrimitive>
        </DialogContent>
      </Dialog>
      {guidanceAgent ? (
        <NotChattableDialog
          agent={guidanceAgent}
          open
          onOpenChange={(open) => {
            if (!open) setGuidanceAgent(null);
          }}
        />
      ) : null}
    </>
  );
}

type SearchRowProps = {
  query: string;
  mode: PaletteMode;
  payload: string;
  projects: ProjectFlat[];
  onQueryChange: (v: string) => void;
  onClose: () => void;
};

function SearchRow({
  query,
  mode,
  payload,
  projects,
  onQueryChange,
  onClose,
}: SearchRowProps) {
  const { t } = useTranslation();
  // Input value 在 command 模式下显示 payload（不含 prefix），保证光标位置干净；
  // onValueChange 反向加回 prefix 写到 query state。底层 query 始终以 prefix 开头 → parseMode 单一真相。
  const inputValue = mode === "command" ? payload : query;
  const handleChange = React.useCallback(
    (v: string) => {
      if (mode === "command") {
        onQueryChange(COMMAND_PREFIX + (v.startsWith(" ") ? v : ` ${v}`));
      } else {
        onQueryChange(v);
      }
    },
    [mode, onQueryChange],
  );

  // 命令模式 + Input payload 空时按 Backspace 的优先级：
  //   1) 有 projectContext → 先清 context（保留命令模式）+ 清 localStorage
  //   2) 无 projectContext → 退出命令模式（删 chip）
  // 多按一次 Backspace 才退出 = 防误删 + 与 chip 操作惯例对齐。
  //
  // Tab 在命令模式下生效（默认模式不消费，走 native）：
  //   Tab → 循环切下一个项目
  //   Shift+Tab → 循环切上一个项目
  // 焦点始终留在 Input；preventDefault + stopPropagation 防止浏览器 native focus
  // 切换以及 cmdk 在 Command 根上消费 Tab。
  const handleKeyDown = React.useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (mode !== "command") return;
      if (e.key === "Backspace" && payload.length === 0) {
        e.preventDefault();
        const ctxStore = useNewChatContextStore.getState();
        if (ctxStore.projectContext) {
          ctxStore.setContext(null);
          clearLastContext();
        } else {
          onQueryChange("");
        }
        return;
      }
      if (e.key === "Tab") {
        e.preventDefault();
        e.stopPropagation();
        cycleProjectShortcut(projects, e.shiftKey ? -1 : 1);
      }
    },
    [mode, payload, projects, onQueryChange],
  );

  return (
    <div className="flex h-14 shrink-0 items-center gap-3 border-b border-border px-5">
      <Search
        className="size-[18px] shrink-0 text-muted-foreground"
        aria-hidden="true"
      />
      {mode === "command" ? (
        <span
          className="inline-flex h-[22px] shrink-0 items-center gap-1 rounded-sm border border-primary bg-primary/10 px-2 font-mono text-2xs font-semibold text-primary"
          aria-label={t("commandPalette.search.commandMode")}
          title={t("commandPalette.search.commandModeTitle")}
        >
          <Terminal className="size-3" aria-hidden="true" />
          {t("commandPalette.search.command")}
        </span>
      ) : null}
      <CommandPrimitive.Input
        autoFocus
        placeholder={
          mode === "command"
            ? t("commandPalette.search.commandPlaceholder")
            : t("commandPalette.search.sessionPlaceholder")
        }
        value={inputValue}
        onValueChange={handleChange}
        onKeyDown={handleKeyDown}
        className="flex-1 bg-transparent text-base outline-none placeholder:text-muted-foreground"
      />
      {query ? (
        <button
          type="button"
          aria-label={t("commandPalette.search.clear")}
          title={t("commandPalette.search.clear")}
          onClick={() => onQueryChange("")}
          className="inline-flex size-[22px] shrink-0 items-center justify-center rounded-sm bg-secondary text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <X className="size-3" aria-hidden="true" />
        </button>
      ) : null}
      <button
        type="button"
        onClick={onClose}
        className="inline-flex h-5 shrink-0 items-center justify-center rounded-sm border border-border bg-secondary px-1.5 font-mono text-2xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        aria-label={t("commandPalette.close")}
      >
        Esc
      </button>
    </div>
  );
}

function emptyText(mode: PaletteMode, payload: string, t: TFunction): string {
  const hasPayload = payload.trim().length > 0;
  if (mode === "command") {
    return hasPayload
      ? t("commandPalette.empty.noCommands")
      : t("commandPalette.empty.commandHint");
  }
  return hasPayload
    ? t("commandPalette.empty.noSessions")
    : t("commandPalette.empty.sessionHint");
}

type SourceGroupProps<T extends CommandItemBase> = {
  source: CommandSource<T>;
  sourcePriority: number;
  query: string;
  ctx: OnSelectCtx;
  onFirstSelectableChange: (sourcePriority: number, itemKeys: string[]) => void;
};

function SourceGroup<T extends CommandItemBase>({
  source,
  sourcePriority,
  query,
  ctx,
  onFirstSelectableChange,
}: SourceGroupProps<T>) {
  const { t } = useTranslation();
  const { items, loading } = source.useItems();
  const q = query.trim();
  const ranked = React.useMemo(() => {
    const withScore = items
      .map((item) => ({ item, score: source.getScore(q, item) }))
      .filter((r) => r.score > 0);
    withScore.sort((a, b) => b.score - a.score);
    // 二次稳定排序: 把同 subHeading 的 item 聚到一起 (先出现的 subHeading 排前面);
    // 没有 subHeading 的当作 "" 单独成组并排到末尾。query 模式下 score 已是主序,
    // 这步只是把"散落到不同分组"的 item 重新汇拢,避免分组分隔条来回插入。
    const order = new Map<string, number>();
    let next = 0;
    for (const r of withScore) {
      const key = r.item.subHeading ?? "";
      if (!order.has(key)) order.set(key, next++);
    }
    withScore.sort((a, b) => {
      const ka = a.item.subHeading ?? "";
      const kb = b.item.subHeading ?? "";
      const oa = order.get(ka) ?? next;
      const ob = order.get(kb) ?? next;
      if (oa !== ob) return oa - ob;
      return b.score - a.score;
    });
    return withScore.slice(0, 50);
  }, [items, q, source]);

  const selectableKeys = React.useMemo(
    () =>
      ranked
        .filter(({ item }) => !(source.isDisabled?.(item) ?? false))
        .map(({ item }) => item.key),
    [ranked, source],
  );
  const selectableKeySignature = selectableKeys.join("\u0000");

  React.useLayoutEffect(() => {
    onFirstSelectableChange(sourcePriority, selectableKeys);
    // selectableKeySignature is the stable semantic dependency; selectableKeys
    // is intentionally omitted because its array identity changes every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onFirstSelectableChange, selectableKeySignature, sourcePriority]);

  const heading = q
    ? t("commandPalette.group.matches", { count: ranked.length })
    : t("commandPalette.group.activeFirst", { count: ranked.length });

  if (loading && ranked.length === 0 && !q) {
    return (
      <div className="px-5 py-8 text-center text-xs text-muted-foreground">
        {t("common.loading")}
      </div>
    );
  }
  if (ranked.length === 0) return null;

  return (
    <>
      <div className="flex items-center justify-between px-4 pb-1 pt-3">
        <span className="text-2xs font-semibold tracking-wide text-muted-foreground">
          {source.heading}
        </span>
        <span className="text-2xs text-muted-foreground/70">{heading}</span>
      </div>
      <CommandPrimitive.Group className="px-1 pb-1">
        {ranked.map(({ item }, idx) => {
          const prev = idx > 0 ? ranked[idx - 1].item.subHeading : undefined;
          const showSub = !!item.subHeading && item.subHeading !== prev;
          const disabled = source.isDisabled?.(item) ?? false;
          return (
            <React.Fragment key={item.key}>
              {showSub ? (
                <div
                  className="px-3 pb-1 pt-2 text-2xs font-semibold uppercase tracking-wider text-muted-foreground"
                  aria-hidden="true"
                >
                  {item.subHeading}
                </div>
              ) : null}
              <CommandPrimitive.Item
                value={item.key}
                disabled={disabled}
                onSelect={() => source.onSelect(item, ctx)}
                className={cn(
                  "group/cmditem flex items-center gap-3 rounded-md px-3 py-2 outline-none transition-colors",
                  disabled ? "cursor-not-allowed" : "cursor-pointer",
                  "data-[selected=true]:bg-accent data-[selected=true]:text-accent-foreground",
                )}
              >
                {source.renderItem(item, { active: false })}
              </CommandPrimitive.Item>
            </React.Fragment>
          );
        })}
      </CommandPrimitive.Group>
    </>
  );
}

// Tab / Shift+Tab 直接操作 new-chat-context-store（焦点不动）。
// 复用 ContextBar 的写库 / localStorage 写入逻辑，保证两套入口（点击 vs 键盘）
// 状态变化一致。
function cycleProjectShortcut(
  projects: ProjectFlat[],
  direction: 1 | -1,
): void {
  if (projects.length === 0) return; // 没有项目时 no-op
  const store = useNewChatContextStore.getState();
  const cur = store.projectContext;
  const curIdx =
    cur == null ? -1 : projects.findIndex((p) => p.id === cur.projectID);

  // 环形顺序：无项目 → 0 → 1 → ... → n-1 → 无项目 → 0 → ...
  // 反向顺序：无项目 → n-1 → ... → 0 → 无项目 → n-1 → ...
  // 当前 projectID 已不在列表里（项目被删）也走"从头开始"分支。
  let next: ProjectFlat | null;
  if (direction === 1) {
    if (cur == null) {
      next = projects[0];
    } else if (curIdx === -1) {
      next = projects[0];
    } else if (curIdx === projects.length - 1) {
      next = null;
    } else {
      next = projects[curIdx + 1];
    }
  } else {
    if (cur == null) {
      next = projects[projects.length - 1];
    } else if (curIdx === -1) {
      next = projects[projects.length - 1];
    } else if (curIdx === 0) {
      next = null;
    } else {
      next = projects[curIdx - 1];
    }
  }

  if (next == null) {
    store.setContext(null);
    clearLastContext();
    return;
  }
  const ctx = {
    projectID: next.id,
    projectName: next.name,
  };
  store.setContext(ctx);
  writeLastContext(ctx);
}

type ContextBarProps = {
  projects: ProjectFlat[];
};

function ContextBar({ projects }: ContextBarProps) {
  const { t } = useTranslation();
  const projectContext = useNewChatContextStore((s) => s.projectContext);
  const setContext = useNewChatContextStore((s) => s.setContext);

  return (
    <div className="flex h-9 shrink-0 items-center gap-2 border-b border-border bg-muted/40 px-5 text-2xs">
      <span className="font-mono text-2xs font-semibold uppercase tracking-wider text-muted-foreground">
        {t("commandPalette.context.title")}
      </span>

      <ProjectChipPicker
        projectContext={projectContext}
        projects={projects}
        onPick={(picked) => {
          if (picked === null) {
            setContext(null);
            clearLastContext();
            return;
          }
          const next = {
            projectID: picked.id,
            projectName: picked.name,
          };
          setContext(next);
          writeLastContext(next);
        }}
      />

      <div className="ml-auto flex items-center gap-3">
        <span className="text-2xs text-muted-foreground">
          {projectContext
            ? t("commandPalette.context.projectModeHint")
            : t("commandPalette.context.freeModeHint")}
        </span>
        <KbdHint kbd="Tab" label={t("commandPalette.context.switchProject")} />
      </div>
    </div>
  );
}

function KbdHint({ kbd, label }: { kbd: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <kbd
        className="inline-flex h-[16px] min-w-[16px] items-center justify-center rounded-sm border border-border bg-secondary px-1 font-mono text-2xs font-medium text-muted-foreground"
        aria-hidden="true"
      >
        {kbd}
      </kbd>
      <span className="text-2xs text-muted-foreground">{label}</span>
    </span>
  );
}

type ProjectChipPickerProps = {
  projectContext: ReturnType<
    typeof useNewChatContextStore.getState
  >["projectContext"];
  projects: ReturnType<typeof useProjectList>["projects"];
  onPick: (
    picked: ReturnType<typeof useProjectList>["projects"][number] | null,
  ) => void;
};

function ProjectChipPicker({
  projectContext,
  projects,
  onPick,
}: ProjectChipPickerProps) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const label = projectContext
    ? projectContext.projectName ||
      t("commandPalette.project.fallbackName", {
        id: projectContext.projectID,
      })
    : t("commandPalette.project.none");

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={t("commandPalette.project.switchAria")}
          title={t("commandPalette.project.switchTitle")}
          className={cn(
            "inline-flex h-[22px] items-center gap-1.5 rounded-sm border bg-card px-2 text-xs font-medium text-foreground outline-none transition-colors hover:bg-accent",
            projectContext
              ? "border-border"
              : "border-border/60 text-muted-foreground",
          )}
        >
          {projectContext ? (
            <Folder
              className="size-[12px] text-primary-text"
              aria-hidden="true"
            />
          ) : (
            <FolderMinus
              className="size-[12px] text-muted-foreground"
              aria-hidden="true"
            />
          )}
          {label}
          <ChevronDown
            className="size-[11px] text-muted-foreground"
            aria-hidden="true"
          />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" sideOffset={4} className="w-[260px] p-1">
        <ProjectPickerItem
          icon={
            <FolderMinus
              className="size-[12px] text-muted-foreground"
              aria-hidden="true"
            />
          }
          label={t("commandPalette.project.none")}
          subtitle={t("commandPalette.context.freeModeHint")}
          selected={!projectContext}
          onSelect={() => {
            onPick(null);
            setOpen(false);
          }}
        />
        {projects.length > 0 ? (
          <div className="my-1 h-px bg-border" aria-hidden="true" />
        ) : null}
        {projects.length === 0 ? (
          <div className="px-2 py-3 text-center text-2xs text-muted-foreground">
            {t("commandPalette.project.empty")}
          </div>
        ) : (
          projects.map((p) => (
            <ProjectPickerItem
              key={p.id}
              icon={
                <Folder
                  className="size-[12px] text-primary-text"
                  aria-hidden="true"
                />
              }
              label={p.name}
              selected={projectContext?.projectID === p.id}
              onSelect={() => {
                onPick(p);
                setOpen(false);
              }}
            />
          ))
        )}
      </PopoverContent>
    </Popover>
  );
}

type ProjectPickerItemProps = {
  icon: React.ReactNode;
  label: string;
  subtitle?: string;
  selected: boolean;
  onSelect: () => void;
};

function ProjectPickerItem({
  icon,
  label,
  subtitle,
  selected,
  onSelect,
}: ProjectPickerItemProps) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className={cn(
        "flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left transition-colors hover:bg-accent",
        selected && "bg-accent/60",
      )}
    >
      <span className="flex size-5 shrink-0 items-center justify-center">
        {icon}
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="truncate text-xs font-medium text-foreground">
          {label}
        </span>
        {subtitle ? (
          <span className="truncate text-2xs text-muted-foreground">
            {subtitle}
          </span>
        ) : null}
      </span>
      {selected ? (
        <Check
          className="size-[12px] shrink-0 text-primary-text"
          aria-hidden="true"
        />
      ) : null}
    </button>
  );
}

function Footer({ mode }: { mode: PaletteMode }) {
  const { t } = useTranslation();
  // 命令模式下 Tab 切上下文的提示放在 ContextBar 右侧（与 chip 同行）；
  // Footer 只承担命令面板通用提示 + 命令模式专属的 ⌫ 清上下文。
  const isCommand = mode === "command";
  // 默认搜索模式右侧展示“新建对话”教学：按键文案读取 cmd.new-chat 当前绑定并按
  // 平台格式化；脱离快捷键 Provider 的兜底场景显示默认 ⌘N（与 ⌘P 触发器“⌘P”
  // 兜底一致，保证提示不显示与用户当前绑定不一致的按键）。
  const shortcuts = useOptionalShortcutsContext();
  const chord = shortcuts?.bindings.get(CMD_NEW_CHAT_ID);
  const newChatShortcut = chord
    ? formatChord(chord, shortcuts!.platform)
    : "⌘N";
  return (
    <div className="flex h-9 shrink-0 items-center gap-3 overflow-hidden border-t border-border bg-muted px-4">
      <FooterHint kbd="↑↓" label={t("commandPalette.footer.navigate")} />
      <FooterHint
        kbd="↵"
        label={
          isCommand
            ? t("commandPalette.footer.create")
            : t("commandPalette.footer.open")
        }
      />
      {isCommand ? (
        <FooterHint kbd="⌫" label={t("commandPalette.footer.clearContext")} />
      ) : null}
      <FooterHint kbd="Esc" label={t("common.close")} />
      <div className="flex-1" />
      {isCommand ? (
        <span className="flex shrink-0 items-center gap-1 text-2xs font-medium text-muted-foreground">
          <Sparkles className="size-[11px] text-primary" aria-hidden="true" />
          {t("commandPalette.search.commandMode")}
        </span>
      ) : (
        <NewChatHint
          kbd={newChatShortcut}
          label={t("commandPalette.footer.newChat")}
        />
      )}
    </div>
  );
}

// 默认搜索模式右侧的“新建对话”教学提示（占用原“✦ 命令面板”状态标识位）。
// 按键胶囊用轻量品牌色强调，但仍是次级信息，不抢列表焦点；纯教学，无点击行为。
function NewChatHint({ kbd, label }: { kbd: string; label: string }) {
  return (
    <>
      <span className="h-4 w-px shrink-0 bg-border" aria-hidden="true" />
      <span className="flex shrink-0 items-center gap-1.5">
        <kbd
          className="inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-sm border border-primary/40 bg-primary-soft px-1.5 font-mono text-2xs font-semibold text-primary-text"
          aria-hidden="true"
        >
          {kbd}
        </kbd>
        <span className="text-2xs font-semibold text-primary-text">
          {label}
        </span>
      </span>
    </>
  );
}

function FooterHint({ kbd, label }: { kbd: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <kbd
        className="inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-sm border border-border bg-card px-1.5 font-mono text-2xs font-medium text-muted-foreground"
        aria-hidden="true"
      >
        {kbd}
      </kbd>
      <span className="text-2xs text-muted-foreground">{label}</span>
    </span>
  );
}
