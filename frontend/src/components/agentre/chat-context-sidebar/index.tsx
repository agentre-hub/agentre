import * as React from "react";
import { FolderTree, GitCompareArrows, ListTree } from "lucide-react";
import { useTranslation } from "react-i18next";

import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import { useSessionLiveBlocks } from "@/stores/chat-streams-store";
import { useFileSettingsStore } from "@/stores/file-settings-store";
import {
  selectIsToolWindowShown,
  useToolWindowLayout,
} from "@/stores/tool-window-layout-store";

import type { chat_svc } from "../../../../wailsjs/go/models";

import { ToolWindowLayout } from "../tool-windows/tool-window-layout";
import type { ToolWindowDefinition } from "../tool-windows/types";

import {
  ChangesToolWindow,
  ChatContextDataContext,
  DirectoryToolWindow,
  OutlineToolWindow,
  type ChatContextData,
} from "./context-windows";
import { deriveOutline, deriveSessionChanges } from "./derive";
import { useGitChanges } from "./views/use-git-changes";
import { useGitState } from "./views/use-git-state";
import { useWorkRoots } from "./views/use-work-roots";

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
  /** 新建会话提示页：不显示图标条与工具窗（见 ToolWindowLayout）。 */
  hidden?: boolean;
  /** 编辑区（对话内容页与文件预览列），由工具窗骨架摆在左边。 */
  children?: React.ReactNode;
};

/**
 * ChatContextToolWindows 把对话上下文的三个视图——大纲 / 变更 / 目录——作为工具窗
 * 挂进对话页的工具窗骨架（spec「工具窗布局」）。
 *
 * 三个工具窗共享的会话级事实（工作根、会话变更、git 快照……）在这一层算一次，
 * 经 context 交给各工具窗：「变更」与「目录」看的是同一个工作根，图标条上的
 * 变更角标与「变更」工具窗的列表是同一份数字。
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
  hidden = false,
  children,
}: Props) {
  const { t } = useTranslation();
  const changesScope = useChatSidebarStore((s) => s.changesScope);
  const setChangesScope = useChatSidebarStore((s) => s.setChangesScope);
  const setWorkRoot = useChatSidebarStore((s) => s.setWorkRoot);
  const changesShown = useToolWindowLayout(selectIsToolWindowShown("changes"));
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

  // 工作根由这一层持有：「变更」与「目录」两个工具窗共享它（spec「工作根」）。
  // root 是给绑定用的实参，workRoot 是拼绝对路径用的全路径。
  const workRoots = useWorkRoots({ sessionId, cwd, messages });
  const workRoot = workRoots.current;
  const root = workRoots.rootArg;

  // 预览面板是工具窗的**兄弟节点**（chat-panel 并排渲染），没有共同父级能把
  // 工作根当 prop 传过去，所以经 store 转发一份——这一层仍是唯一的写入者。
  React.useEffect(() => {
    setWorkRoot(sessionId, workRoot);
  }, [setWorkRoot, sessionId, workRoot]);

  // 正在跑的那一轮的块还没进 messages（发送时插的 assistant 是空占位，落定才被
  // 真正的消息替换），它们住在 chat-streams-store。「变更」读的是**会话级**
  // 事实，用户轮 / 自主续轮 / 后台活动轮的在流一并收进来 —— 只读 messages 就等于
  // AI 改文件的整个过程里这一页恒为空。只订阅块集合 —— 文字 delta 不改变哪个
  // 文件被改过，不该让下面的派生每帧重扫。
  const liveBlocks = useSessionLiveBlocks(sessionId);

  const outline = React.useMemo(() => deriveOutline(messages), [messages]);
  // 「本次会话」的行只从 canonical 块派生，当前工作根之外的路径在这里就被挡掉
  // （spec「归属过滤」）——不读 git，因此与有没有提交无关。
  const sessionChanges = React.useMemo(
    () => deriveSessionChanges(messages, workRoot, liveBlocks),
    [messages, workRoot, liveBlocks],
  );

  // 根切换器上每个根各自的变更数（spec「呈现」）：与行用的是同一个派生函数，
  // 数字因此和切过去之后看到的行数一致。
  const changeCounts = React.useMemo(() => {
    const counts = new Map<string, number>();
    for (const r of workRoots.roots) {
      counts.set(
        r.path,
        deriveSessionChanges(messages, r.path, liveBlocks).length,
      );
    }
    return counts;
  }, [messages, workRoots.roots, liveBlocks]);

  const turnToMessageId = React.useMemo(() => {
    const m = new Map<number, number>();
    let turn = 0;
    for (const msg of messages) {
      if (msg.role === "user") {
        turn += 1;
        m.set(turn, msg.id);
      }
    }
    return m;
  }, [messages]);

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

  // git 取数挂在这一层：「变更」的「未提交」档、它的角标、以及「目录」的状态
  // 叠加要的是同一份快照，共用一个 hook 实例就只会有一次在途请求（决策 13：只有
  // 需要它的工具窗显示时才打后端）。
  const git = useGitChanges({
    sessionId,
    cwd,
    root,
    enabled: !hidden && (changesShown || directoryShown),
  });

  // 分支状态只有「目录」那一行需要（决策 7），因此只在它显示时取数。
  const gitState = useGitState({
    sessionId,
    cwd,
    root,
    enabled: !hidden && directoryShown,
  });

  // 「未提交」档只在这个会话真有一个 git 工作目录时才存在（决策 11）：非 git
  // 仓库下它失去意义，只剩「本次会话」一档，而不是留一个空页。持久化的档位落在
  // 一个不存在的档上时同样钳回「本次会话」。
  const uncommittedAvailable = cwd !== "" && !git.notARepo;
  const scope = uncommittedAvailable ? changesScope : "session";
  // 「变更」的数字是**当前档**的文件数；「未提交」档的数据尚未就绪时为 null。
  const changesCount = scope === "session" ? sessionChanges.length : git.count;

  const onJumpToTurn = React.useCallback(
    (turn: number) => {
      const mid = turnToMessageId.get(turn);
      if (mid != null) onJumpToMessage(mid);
    },
    [turnToMessageId, onJumpToMessage],
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
      onJumpToTurn,
      workRoots,
      changeCounts,
      sessionChanges,
      scope,
      onScopeChange: setChangesScope,
      uncommittedAvailable,
      git,
      gitState,
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
      onJumpToTurn,
      workRoots,
      changeCounts,
      sessionChanges,
      scope,
      setChangesScope,
      uncommittedAvailable,
      git,
      gitState,
    ],
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
    ],
    [t, changesCount],
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
