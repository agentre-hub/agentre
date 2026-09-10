// 命令面板本体:搜索框 + 分组结果 + 键盘导航,以及把选中项派发出去。
//
// 它渲染的那些零件都在这个目录下:search-row / source-group /
// project-chip-picker / context-bar / footer。原先它们与本体挤在同一个文件里
// (879 行,本体只占 236 行)。

import * as React from "react";
import { Command as CommandPrimitive } from "cmdk";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router-dom";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@agentre-hub/agentre-ui";
import { useProjectList } from "@/hooks/use-project-list";
import type { AgentSlim } from "@/hooks/use-chat-agents";
import { cn } from "@/lib/utils";
import { useCommandPaletteStore } from "@/stores/command-palette-store";
import { readLastContext } from "@/stores/new-chat-context-persistence";
import { useNewChatContextStore } from "@/stores/new-chat-context-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";

import { NotChattableDialog } from "../not-chattable";
import { parseMode, type PaletteMode } from "./mode";
import { chatSessionsSource } from "./sources/chat-sessions-source";
import { navigationSource } from "./sources/navigation-source";
import { newAgentSource } from "./sources/new-agent-source";
import { newChatSource } from "./sources/new-chat-source";
import { newProjectChatSource } from "./sources/new-project-chat-source";
import type { CommandSource, OnSelectCtx } from "./types";

import { ContextBar } from "./context-bar";
import { Footer } from "./footer";
import { SearchRow } from "./search-row";
import { SourceGroup } from "./source-group";

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
