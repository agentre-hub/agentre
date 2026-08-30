import * as React from "react";
import { useTranslation } from "react-i18next";

import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import {
  useChatStreamsStore,
  type ChatBlockData,
} from "@/stores/chat-streams-store";

import type { chat_svc } from "../../../../wailsjs/go/models";

import { ResizableSidebar } from "../resizable-sidebar";

import { deriveOutline, deriveSessionChanges } from "./derive";
import { RootFollowNotice } from "./root-follow-notice";
import { RootSwitcher } from "./root-switcher";
import { TabBar } from "./tab-bar";
import { ChangesPanel } from "./views/changes-panel";
import { DirectoryPanel } from "./views/directory-panel";
import { OutlineView } from "./views/outline-view";
import { useGitChanges } from "./views/use-git-changes";
import { useGitState } from "./views/use-git-state";
import { useWorkRoots } from "./views/use-work-roots";

type Msg = chat_svc.ChatMessage;

const EMPTY_LIVE_BLOCKS: ChatBlockData[] = [];

type Props = {
  sessionId: number;
  messages: Msg[];
  activeMessageId: number | null;
  onJumpToMessage: (messageId: number) => void;
  cwd?: string;
  remote?: boolean;
  /** R10：cwd 为空时的结构化原因，透给「目录」模式渲染专用空态。 */
  cwdUnavailableReason?: string;
  /** 会话绑定的项目 id，R10 空态的"指定本机路径"入口据此调用 ProjectSetLocalPath。 */
  projectId?: number;
  /** 指定路径成功后的回调——调用方据此重新 LoadSession。 */
  onCwdSpecified?: () => void;
};

export function ChatContextSidebar({
  sessionId,
  messages,
  activeMessageId,
  onJumpToMessage,
  cwd = "",
  remote = false,
  cwdUnavailableReason,
  projectId,
  onCwdSpecified,
}: Props) {
  const { t } = useTranslation();
  const activeTab = useChatSidebarStore((s) => s.activeTab);
  const setActiveTab = useChatSidebarStore((s) => s.setActiveTab);
  const changesScope = useChatSidebarStore((s) => s.changesScope);
  const setChangesScope = useChatSidebarStore((s) => s.setChangesScope);
  const setWorkRoot = useChatSidebarStore((s) => s.setWorkRoot);

  // 工作根由这一层持有：「变更」页与「目录」页共享它，切一级 tab 不改变它
  // （spec「工作根」）。root 是给绑定用的实参，workRoot 是拼绝对路径用的全路径。
  const workRoots = useWorkRoots({ sessionId, cwd, messages });
  const workRoot = workRoots.current;
  const root = workRoots.rootArg;

  // 预览面板是本侧栏的**兄弟节点**（chat-panel 并排渲染），没有共同父级能把
  // 工作根当 prop 传过去，所以经 store 转发一份——侧栏仍是唯一的写入者。
  React.useEffect(() => {
    setWorkRoot(sessionId, workRoot);
  }, [setWorkRoot, sessionId, workRoot]);

  // 正在跑的那一轮的块还没进 messages（发送时插的 assistant 是空占位，落定才被
  // 真正的消息替换），它们住在 chat-streams-store。「变更」页读的是**会话级**
  // 事实，用户轮 / 自主续轮 / 后台活动轮的在流一并收进来 —— 只读 messages 就等于
  // AI 改文件的整个过程里这一页恒为空。直接订阅 store 而不是让 chat-panel 再传
  // 一个 prop：本侧栏本就自持 sidebar / session-status 两个 store。
  const sessionStreams = useChatStreamsStore((s) =>
    sessionId ? (s.streams.get(sessionId) ?? null) : null,
  );
  const liveBlocks = React.useMemo(() => {
    if (!sessionStreams) return EMPTY_LIVE_BLOCKS;
    return Array.from(sessionStreams.values()).flatMap((s) => s.liveBlocks);
  }, [sessionStreams]);

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

  // git 取数挂在这一层：「变更」页的「未提交」档、它的 tab 角标、以及「目录」页
  // 的状态叠加要的是同一份快照，共用一个 hook 实例就只会有一次在途请求
  // （决策 13：只有需要它的页可见时才打后端）。
  const git = useGitChanges({
    sessionId,
    cwd,
    root,
    enabled: activeTab === "changes" || activeTab === "directory",
  });

  // 分支状态只有「目录」页那一行需要（决策 7），因此只在它可见时取数。
  const gitState = useGitState({
    sessionId,
    cwd,
    root,
    enabled: activeTab === "directory",
  });

  // 「未提交」档只在这个会话真有一个 git 工作目录时才存在（决策 11）：非 git
  // 仓库下它失去意义，该行只剩「本次会话」一档，而不是留一个空页。持久化的
  // 档位落在一个不存在的档上时同样钳回「本次会话」。
  const uncommittedAvailable = cwd !== "" && !git.notARepo;
  const scope = uncommittedAvailable ? changesScope : "session";

  const scrollRef = React.useRef<HTMLDivElement>(null);

  // resolvedActiveId 变化时把对应 outline 行推到滚动区域底部，让右侧进度跟随 transcript。
  React.useEffect(() => {
    if (resolvedActiveId == null) return;
    const container = scrollRef.current;
    if (!container) return;
    const row = container.querySelector<HTMLElement>(
      `[data-outline-message-id="${resolvedActiveId}"]`,
    );
    if (row) row.scrollIntoView({ block: "end", inline: "nearest" });
  }, [resolvedActiveId, activeTab]);

  return (
    <ResizableSidebar
      persistenceKey="chat-context"
      ariaLabel={t("chatContext.sidebar")}
      edge="left"
      defaultWidth={240}
      className="h-full"
    >
      <RootSwitcher
        roots={workRoots.roots}
        current={workRoot}
        pinned={workRoots.pinned}
        changeCounts={changeCounts}
        onSelect={workRoots.select}
      />
      {workRoots.followedTo ? (
        <RootFollowNotice
          root={workRoots.followedTo}
          onUndo={workRoots.stayInMain}
        />
      ) : null}
      <TabBar
        active={activeTab}
        onChange={setActiveTab}
        outlineCount={outline.length}
        changesCount={scope === "session" ? sessionChanges.length : git.count}
      />
      {/*
        「变更」页与「目录」页各自带第二行 chrome，滚动容器在各自的面板内部；
        「大纲」页仍用这里的滚动容器（scrollRef 要拿它做 scrollIntoView）。
      */}
      {activeTab === "outline" ? (
        <div ref={scrollRef} className="min-h-0 flex-1 overflow-auto">
          <OutlineView
            items={outline}
            activeMessageId={resolvedActiveId}
            onSelect={onJumpToMessage}
          />
        </div>
      ) : activeTab === "changes" ? (
        <ChangesPanel
          sessionId={sessionId}
          rows={sessionChanges}
          cwd={workRoot}
          remote={remote}
          scope={scope}
          onScopeChange={setChangesScope}
          uncommittedAvailable={uncommittedAvailable}
          git={git.state}
          onRetry={git.reload}
          onJumpToTurn={(turn) => {
            const mid = turnToMessageId.get(turn);
            if (mid != null) onJumpToMessage(mid);
          }}
        />
      ) : (
        <DirectoryPanel
          sessionId={sessionId}
          cwd={workRoot}
          root={root}
          remote={remote}
          gitState={gitState}
          gitChanges={git.overlayChanges}
          cwdUnavailableReason={cwdUnavailableReason}
          projectId={projectId}
          onCwdSpecified={onCwdSpecified}
        />
      )}
    </ResizableSidebar>
  );
}
