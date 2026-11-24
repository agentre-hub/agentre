import * as React from "react";

import type { ChangesScope } from "@/stores/chat-sidebar-store";

import type { ChangeRow, OutlineItem } from "./derive";
import { RootFollowNotice } from "./root-follow-notice";
import { RootSwitcher } from "./root-switcher";
import { ChangesPanel } from "./views/changes-panel";
import { DirectoryPanel } from "./views/directory-panel";
import { OutlineView } from "./views/outline-view";
import type { GitChanges } from "./views/use-git-changes";
import type { GitState } from "./views/use-git-state";
import type { WorkRoots } from "./views/use-work-roots";

/**
 * 大纲 / 变更 / 目录三个工具窗共享的会话级事实，由 ChatContextToolWindows 算一次。
 * 值是 memo 过的：流式输出期间文字 delta 不改变其中任何一项，工具窗不跟着重渲染。
 */
export type ChatContextData = {
  sessionId: number;
  /** 会话工作目录（不是当前工作根）。 */
  cwd: string;
  remote: boolean;
  cwdUnavailableReason?: string;
  projectId?: number;
  onCwdSpecified?: () => void;
  outline: OutlineItem[];
  /** 转录当前所在那一轮的用户消息 id；大纲据此高亮并跟随滚动。 */
  activeOutlineId: number | null;
  onJumpToMessage: (messageId: number) => void;
  onJumpToTurn: (turn: number) => void;
  workRoots: WorkRoots;
  /** 每个工作根下「本次会话」的变更数，根切换器用。 */
  changeCounts: Map<string, number>;
  sessionChanges: ChangeRow[];
  /** 已生效的「变更」档位（非 git 仓库时已钳到「本次会话」）。 */
  scope: ChangesScope;
  onScopeChange: (scope: ChangesScope) => void;
  uncommittedAvailable: boolean;
  git: GitChanges;
  gitState: GitState | null;
};

export const ChatContextDataContext =
  React.createContext<ChatContextData | null>(null);

function useChatContextData(): ChatContextData {
  const data = React.useContext(ChatContextDataContext);
  if (data === null) {
    throw new Error(
      "chat-context tool windows must render inside ChatContextToolWindows",
    );
  }
  return data;
}

/** 「大纲」工具窗：按轮列出用户消息，点击跳到那一轮，高亮跟随转录。 */
export function OutlineToolWindow() {
  const { outline, activeOutlineId, onJumpToMessage } = useChatContextData();
  const scrollRef = React.useRef<HTMLDivElement>(null);

  // 高亮行变化时把它推到滚动区域底部，让大纲的进度跟随转录。
  React.useEffect(() => {
    if (activeOutlineId == null) return;
    const row = scrollRef.current?.querySelector<HTMLElement>(
      `[data-outline-message-id="${activeOutlineId}"]`,
    );
    if (row) row.scrollIntoView({ block: "end", inline: "nearest" });
  }, [activeOutlineId]);

  return (
    <div ref={scrollRef} className="min-h-0 flex-1 overflow-auto">
      <OutlineView
        items={outline}
        activeMessageId={activeOutlineId}
        onSelect={onJumpToMessage}
      />
    </div>
  );
}

/**
 * 工作根切换器与自动跟随提示行：「变更」与「目录」都按当前工作根取数，因此两个
 * 工具窗各自顶上带一份（同一个状态）。工作根切换挪到对话标题栏之前，它就留在这里。
 */
function WorkRootBar() {
  const { workRoots, changeCounts } = useChatContextData();
  return (
    <>
      <RootSwitcher
        roots={workRoots.roots}
        current={workRoots.current}
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
    </>
  );
}

/** 「变更」工具窗：本次会话 / 未提交两档。 */
export function ChangesToolWindow() {
  const d = useChatContextData();
  return (
    <>
      <WorkRootBar />
      <ChangesPanel
        sessionId={d.sessionId}
        rows={d.sessionChanges}
        cwd={d.workRoots.current}
        remote={d.remote}
        scope={d.scope}
        onScopeChange={d.onScopeChange}
        uncommittedAvailable={d.uncommittedAvailable}
        git={d.git.state}
        onRetry={d.git.reload}
        onJumpToTurn={d.onJumpToTurn}
      />
    </>
  );
}

/** 「目录」工具窗：带 git 状态叠加的目录树。 */
export function DirectoryToolWindow() {
  const d = useChatContextData();
  return (
    <>
      <WorkRootBar />
      <DirectoryPanel
        sessionId={d.sessionId}
        cwd={d.workRoots.current}
        root={d.workRoots.rootArg}
        remote={d.remote}
        gitState={d.gitState}
        gitChanges={d.git.overlayChanges}
        cwdUnavailableReason={d.cwdUnavailableReason}
        projectId={d.projectId}
        onCwdSpecified={d.onCwdSpecified}
      />
    </>
  );
}
