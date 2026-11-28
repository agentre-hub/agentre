import * as React from "react";

import { RelationsToolWindow } from "../tool-windows/relations-tool-window";
import {
  SessionInfoToolWindow,
  type SessionInfoData,
} from "../tool-windows/session-info-tool-window";
import { TerminalToolWindow } from "../tool-windows/terminal-tool-window";
import type { SessionRelations } from "../tool-windows/use-session-relations";
import { WebPreviewToolWindow } from "../tool-windows/web-preview-tool-window";

import type { OutlineItem } from "./derive";
import { ChangesPanel } from "./views/changes-panel";
import { DirectoryPanel } from "./views/directory-panel";
import { OutlineView } from "./views/outline-view";
import type { GitChanges } from "./views/use-git-changes";
import type { GitState } from "./views/use-git-state";

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
  /** 会话当前工作根的绝对路径（没有工作目录时为空串）。 */
  workRoot: string;
  /** 当前工作根的绑定实参（主根 = 空串，见 useSessionRoot）。 */
  root: string;
  /** 会话有一个 git 工作目录（「变更」才列得出东西）。 */
  gitAvailable: boolean;
  git: GitChanges;
  gitState: GitState | null;
  /** 「关联」工具窗的四组数据：图标条上的提醒圆点、标题栏数量与窗口读同一份。 */
  relations: SessionRelations;
  /** 「会话信息」工具窗的各行数据（输入框旁控件的同一份状态 + 由消息派生的轮数与累计用量）。 */
  sessionInfo: SessionInfoData;
  /** 立刻重取会话的进程与端口（网页预览的端口标签来源）；宿主没给时不重取。 */
  refreshProcesses?: () => void;
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

/** 「变更」工具窗：工作区未提交的文件。 */
export function ChangesToolWindow() {
  const d = useChatContextData();
  return (
    <ChangesPanel
      sessionId={d.sessionId}
      cwd={d.workRoot}
      remote={d.remote}
      gitAvailable={d.gitAvailable}
      git={d.git.state}
      onRetry={d.git.reload}
    />
  );
}

/** 「目录」工具窗：带 git 状态叠加的目录树。 */
export function DirectoryToolWindow() {
  const d = useChatContextData();
  return (
    <DirectoryPanel
      sessionId={d.sessionId}
      cwd={d.workRoot}
      root={d.root}
      remote={d.remote}
      gitState={d.gitState}
      gitChanges={d.git.overlayChanges}
      cwdUnavailableReason={d.cwdUnavailableReason}
      projectId={d.projectId}
      onCwdSpecified={d.onCwdSpecified}
    />
  );
}

/** 「终端」工具窗：本会话的终端标签；面板由常驻的 SessionTerminalsHost 持有。 */
export function TerminalToolWindowEntry() {
  const { sessionId } = useChatContextData();
  return <TerminalToolWindow sessionId={sessionId} />;
}

/** 「关联」工具窗：关联任务、派发我的、我派发的、同一份代码上在跑。 */
export function RelationsToolWindowEntry() {
  const { sessionId, relations } = useChatContextData();
  return <RelationsToolWindow sessionId={sessionId} relations={relations} />;
}

/** 「会话信息」工具窗：只读键值列表。 */
export function SessionInfoToolWindowEntry() {
  const { sessionId, sessionInfo } = useChatContextData();
  return <SessionInfoToolWindow sessionId={sessionId} info={sessionInfo} />;
}

/** 「网页预览」工具窗：会话监听的端口成标签；远端会话标出执行设备。 */
export function WebPreviewToolWindowEntry() {
  const { sessionId, remote, sessionInfo, refreshProcesses } =
    useChatContextData();
  return (
    <WebPreviewToolWindow
      sessionId={sessionId}
      remote={remote}
      deviceName={sessionInfo.session?.deviceName ?? ""}
      onRefreshPorts={refreshProcesses}
    />
  );
}
