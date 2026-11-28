import * as React from "react";
import {
  FolderTree,
  GitCompareArrows,
  Globe,
  Info,
  Link2,
  ListTree,
  SquareTerminal,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import { useFileSettingsStore } from "@/stores/file-settings-store";
import { useSessionRoot } from "@/stores/session-root-store";
import {
  hasLiveSessionTerminal,
  useSessionTerminalsStore,
} from "@/stores/session-terminals-store";
import {
  selectIsToolWindowShown,
  useToolWindowLayout,
} from "@/stores/tool-window-layout-store";

import type { chat_svc } from "../../../../wailsjs/go/models";

import { ToolWindowLayout } from "../tool-windows/tool-window-layout";
import type { ToolWindowDefinition } from "../tool-windows/types";
import type { SessionInfoData } from "../tool-windows/session-info-tool-window";
import { sumSessionUsage } from "../tool-windows/session-usage";
import { useSessionRelations } from "../tool-windows/use-session-relations";
import { useWebPreviewStore } from "../tool-windows/web-preview-store";
import type { SessionProcess } from "../background-tasks/types";

import {
  ChangesToolWindow,
  ChatContextDataContext,
  DirectoryToolWindow,
  OutlineToolWindow,
  RelationsToolWindowEntry,
  SessionInfoToolWindowEntry,
  TerminalToolWindowEntry,
  WebPreviewToolWindowEntry,
  type ChatContextData,
} from "./context-windows";
import { deriveOutline } from "./derive";
import { useGitChanges } from "./views/use-git-changes";
import { useGitState } from "./views/use-git-state";
import { useSessionRootSync } from "./views/use-session-root-sync";

type Msg = chat_svc.ChatMessage;

type Props = {
  sessionId: number;
  messages: Msg[];
  activeMessageId: number | null;
  onJumpToMessage: (messageId: number) => void;
  cwd?: string;
  remote?: boolean;
  /** R10：cwd 为空时的结构化原因，透给「目录」工具窗渲染专用空态。 */
  cwdUnavailableReason?: string;
  /** 会话绑定的项目 id，R10 空态的"指定本机路径"入口据此调用 ProjectSetLocalPath。 */
  projectId?: number;
  /** 指定路径成功后的回调——调用方据此重新 LoadSession。 */
  onCwdSpecified?: () => void;
  /**
   * 「会话信息」工具窗的输入：会话详情与输入框旁控件的同一份状态。轮数与累计用量由这
   * 一层从 messages 派生。缺省 = 还没有会话详情，窗口里只剩不依赖它的行。
   */
  sessionInfo?: Omit<SessionInfoData, "turns" | "usage">;
  /**
   * 会话留下的存活进程（与后台弹层「进程与端口」同一份）。监听中的端口自动成「网页预览」
   * 的标签；窗口没打开时也同步，打开时标签已就绪。缺省 = 此刻不知道进程列表（面板收着、
   * 取数失败）：标签原样留着——不知道不等于端口消失。
   */
  processes?: readonly SessionProcess[];
  /** 立刻重取进程与端口（网页预览「重新加载」一个已不再监听的标签时）。 */
  onRefreshProcesses?: () => void;
  /** 新建会话提示页：不显示图标条与工具窗（见 ToolWindowLayout）。 */
  hidden?: boolean;
  /** 编辑区（对话内容页与文件预览列），由工具窗骨架摆在左边。 */
  children?: React.ReactNode;
};

/**
 * ChatContextToolWindows 把对话上下文的三个视图——大纲 / 变更 / 目录——作为工具窗
 * 挂进对话页的工具窗骨架（spec「工具窗布局」）。
 *
 * 三个工具窗共享的会话级事实（会话变更、git 快照……）在这一层算一次，经 context
 * 交给各工具窗：图标条上的变更角标与「变更」工具窗的列表是同一份数字。
 *
 * 会话当前工作根是会话级的（useSessionRoot，标题栏胶囊是唯一切换点）：这一层
 * 挂着它的取数与推送订阅（useSessionRootSync），「变更」「目录」都按它取数。
 */
function ChatContextToolWindowsImpl({
  sessionId,
  messages,
  activeMessageId,
  onJumpToMessage,
  cwd = "",
  remote = false,
  cwdUnavailableReason,
  projectId,
  onCwdSpecified,
  sessionInfo: sessionInfoInput,
  processes,
  onRefreshProcesses,
  hidden = false,
  children,
}: Props) {
  const { t } = useTranslation();
  const directoryShown = useToolWindowLayout(
    selectIsToolWindowShown("directory"),
  );

  // 「单击文件时」这个设置由这一层读入：设置页不是唯一的读入口（用户可能从没
  // 进过设置页），而文件面板的每一行都要按它分流。与通知设置同形——由**用到它
  // 的那个常驻组件**自己 load 一次，store 之后由设置页的写入更新。
  const loadFileSettings = useFileSettingsStore((s) => s.load);
  React.useEffect(() => {
    void loadFileSettings();
  }, [loadFileSettings]);

  // 会话当前工作根（spec「会话级当前工作根」）：root 是给绑定用的实参（主根 =
  // 空串），workRoot 是拼绝对路径用的全路径。
  useSessionRootSync({ sessionId, cwd });
  const sessionRoot = useSessionRoot(sessionId);
  const workRoot = sessionRoot.current;
  const root = sessionRoot.rootArg;

  const outline = React.useMemo(() => deriveOutline(messages), [messages]);
  // messageIdToTurnUserId 把任意 message id 映射回它所在 turn 的 user 消息 id，
  // 让 outline 高亮在「问–答」整段区间内都锚定在同一行，直到下一个 user 消息出现。
  const messageIdToTurnUserId = React.useMemo(() => {
    const m = new Map<number, number>();
    let anchor: number | null = null;
    for (const msg of messages) {
      if (msg.role === "user") anchor = msg.id;
      if (anchor != null) m.set(msg.id, anchor);
    }
    return m;
  }, [messages]);

  const resolvedActiveId =
    activeMessageId != null
      ? (messageIdToTurnUserId.get(activeMessageId) ?? null)
      : null;

  // git 取数挂在这一层：「变更」的列表、图标条上的角标与「目录」的状态叠加要的
  // 是同一份快照，共用一个 hook 实例就只会有一次在途请求。角标是未提交的文件数，
  // 变更窗收着也要有（「侧栏关着也能看到改了几个文件」），所以会话页在就取数。
  const git = useGitChanges({
    sessionId,
    cwd,
    root,
    enabled: !hidden,
  });

  // 分支状态只有「目录」那一行需要（决策 7），因此只在它显示时取数。
  const gitState = useGitState({
    sessionId,
    cwd,
    root,
    enabled: !hidden && directoryShown,
  });

  // 只有会话真有一个 git 工作目录时才列得出变动；不是 git 仓库或没有工作目录时
  // 「变更」给一句说明，角标不显示（git.count 此时为 null）。
  const gitAvailable = cwd !== "" && !git.notARepo;
  const changesCount = git.count;

  // 「关联」四组数据在这一层取一次：窗口收起时图标条也要它的提醒圆点与标题数量。
  const relations = useSessionRelations({ sessionId, enabled: !hidden });

  // 轮数 = 用户消息数；累计用量只读消息自己的记录列（子代理用量不计入）。
  const turns = React.useMemo(
    () => messages.filter((m) => m.role === "user").length,
    [messages],
  );
  const usage = React.useMemo(() => sumSessionUsage(messages), [messages]);
  const sessionInfo = React.useMemo<SessionInfoData>(
    () => ({
      session: null,
      ...sessionInfoInput,
      turns,
      usage,
    }),
    [sessionInfoInput, turns, usage],
  );

  const data = React.useMemo<ChatContextData>(
    () => ({
      sessionId,
      cwd,
      remote,
      cwdUnavailableReason,
      projectId,
      onCwdSpecified,
      outline,
      activeOutlineId: resolvedActiveId,
      onJumpToMessage,
      workRoot,
      root,
      gitAvailable,
      git,
      gitState,
      relations,
      sessionInfo,
      refreshProcesses: onRefreshProcesses,
    }),
    [
      sessionId,
      cwd,
      remote,
      cwdUnavailableReason,
      projectId,
      onCwdSpecified,
      outline,
      resolvedActiveId,
      onJumpToMessage,
      workRoot,
      root,
      gitAvailable,
      git,
      gitState,
      relations,
      sessionInfo,
      onRefreshProcesses,
    ],
  );

  // 监听中的端口 → 网页预览的标签。一个端口只取第一个监听它的进程的命令。
  const syncPorts = useWebPreviewStore((s) => s.syncPorts);
  React.useEffect(() => {
    if (hidden || processes === undefined) return;
    const seen = new Set<number>();
    const entries: { port: number; command: string }[] = [];
    for (const p of processes) {
      for (const port of p.ports) {
        if (seen.has(port)) continue;
        seen.add(port);
        entries.push({ port, command: p.command });
      }
    }
    syncPorts(sessionId, entries);
  }, [hidden, processes, sessionId, syncPorts]);

  // 图标条上终端图标的运行中圆点：本会话有存活的终端进程。
  const terminalRunning = useSessionTerminalsStore((s) =>
    hasLiveSessionTerminal(s.bySession[sessionId]),
  );

  const windows = React.useMemo<ToolWindowDefinition[]>(
    () => [
      {
        id: "outline",
        label: t("chatContext.outline.label"),
        icon: ListTree,
        content: <OutlineToolWindow />,
      },
      {
        id: "changes",
        label: t("chatContext.changes.label"),
        icon: GitCompareArrows,
        count: changesCount,
        content: <ChangesToolWindow />,
      },
      {
        id: "directory",
        label: t("chatContext.directory.label"),
        icon: FolderTree,
        content: <DirectoryToolWindow />,
      },
      {
        id: "webPreview",
        label: t("webPreviewToolWindow.label"),
        icon: Globe,
        content: <WebPreviewToolWindowEntry />,
      },
      {
        id: "relations",
        label: t("relationsToolWindow.label"),
        icon: Link2,
        count: relations.count,
        countBadge: false,
        alert: relations.overlapAlert,
        content: <RelationsToolWindowEntry />,
      },
      {
        id: "sessionInfo",
        label: t("sessionInfoToolWindow.label"),
        icon: Info,
        content: <SessionInfoToolWindowEntry />,
      },
      {
        id: "terminal",
        label: t("terminalToolWindow.label"),
        icon: SquareTerminal,
        running: terminalRunning,
        content: <TerminalToolWindowEntry />,
      },
    ],
    [t, changesCount, terminalRunning, relations.count, relations.overlapAlert],
  );

  return (
    <ChatContextDataContext.Provider value={data}>
      <ToolWindowLayout windows={windows} hidden={hidden}>
        {children}
      </ToolWindowLayout>
    </ChatContextDataContext.Provider>
  );
}

export const ChatContextToolWindows = ChatContextToolWindowsImpl;
