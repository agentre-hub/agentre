import * as React from "react";

import {
  FilePreviewPanel as SharedFilePreviewPanel,
  ResizableSidebar,
  cn,
  previewNeedsMonaco,
  useUiTranslation,
  type FilePreviewFailure,
  type FilePreviewFailureKind,
  type FilePreviewPorts,
} from "@agentre-hub/agentre-ui";

import {
  WorkspaceFsGitFileContent,
  WorkspaceFsReadFile,
} from "@/../wailsjs/go/app/App";
import type { chat_svc } from "@/../wailsjs/go/models";
import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import {
  selectActivePreviewTab,
  useFilePreviewTabsStore,
} from "@/stores/file-preview-tabs-store";
import { useSessionStatus } from "@/stores/session-status-store";

import { FileTypeIcon } from "../file-type-icon";

import { useMonaco } from "./use-monaco";

type Props = {
  sessionId: number;
  /**
   * 本会话的消息。只有「本次会话」档的工具 diff 用它：那一档的内容全部来自消息里
   * 的 canonical 块，一次后端调用都不打（spec「与『有没有提交』无关」）。
   */
  messages?: chat_svc.ChatMessage[];
  /**
   * 会话工作目录。多工作根会话下真正生效的是侧栏当前选中的那个根（经
   * chat-sidebar-store 转发），这里只是它的兜底。
   */
  cwd?: string;
};

/** 文件身份图标是宿主的资产（扩展名目录 + 色调 token），经 props 交给包里那份面板。 */
function renderFileIcon(path: string, slot: "tab" | "overflow" | "header") {
  const testId =
    slot === "tab"
      ? "preview-tab-file-icon"
      : slot === "overflow"
        ? "preview-overflow-file-icon"
        : "file-preview-header-icon";
  return <FileTypeIcon path={path} testId={testId} />;
}

/**
 * 桌面端预览面板的**装配根**：面板本身（标签条、header、七个态的渲染）那一份在
 * `@agentre-hub/agentre-ui`（跨端共享），这里只做四件宿主自己的事 ——
 *
 *   1. 把 Wails 绑定接成取数端口（`WorkspaceFsReadFile` / `WorkspaceFsGitFileContent`
 *      的会话 + 工作根实参在闭包里带着，包那侧只认 relPath）；
 *   2. 把 `file-preview-tabs-store` 的这一会话的标签与动作映射成 props；
 *   3. 装载 Monaco（装载器用 Vite 的 `?worker`，进不了纯 tsc 构建的包）并注进去；
 *   4. **给它一个容器**：桌面端这一栏是右侧栏里那条可拖宽的 ResizableSidebar
 *      （独立 persistenceKey），关掉最后一个标签时先播 200ms 出场动画再收起。
 *      「开在哪、多宽、能不能拖」是宿主的布局问题（spec 决策 3），控制台那侧的
 *      容器是详情列里一条定宽 420、不可拖的分栏。
 *
 * 工作根的持有者是侧栏（chat-sidebar-store），侧栏还没写过（例如被收起）时回落到
 * 会话 cwd；绑定的 root 实参在当前根就是会话 cwd 时传空串（后端把空串解释成会话
 * cwd）。轮次结束（doneTick）是「文件可能变了」的唯一强信号，接成包那侧的
 * `refreshToken`。
 */
export function FilePreviewPanel({ sessionId, messages, cwd = "" }: Props) {
  const { t: uiT } = useUiTranslation();
  const entry = useFilePreviewTabsStore(
    (s) => s.previewTabsBySession[sessionId],
  );
  const activeTab = useFilePreviewTabsStore((s) =>
    selectActivePreviewTab(s, sessionId),
  );
  const setPreviewSegment = useFilePreviewTabsStore((s) => s.setPreviewSegment);
  const activatePreviewTab = useFilePreviewTabsStore(
    (s) => s.activatePreviewTab,
  );
  const promoteActivePreviewTab = useFilePreviewTabsStore(
    (s) => s.promoteActivePreviewTab,
  );
  const togglePreviewTabPin = useFilePreviewTabsStore(
    (s) => s.togglePreviewTabPin,
  );
  const closePreviewTab = useFilePreviewTabsStore((s) => s.closePreviewTab);
  const closeOtherPreviewTabs = useFilePreviewTabsStore(
    (s) => s.closeOtherPreviewTabs,
  );
  const closeAllPreviewTabs = useFilePreviewTabsStore(
    (s) => s.closeAllPreviewTabs,
  );

  const workRoot = useChatSidebarStore(
    (s) => s.workRootBySession[sessionId] ?? "",
  );
  const root = workRoot === "" ? cwd : workRoot;
  const rootArg = root === cwd ? "" : root;

  // 工具 diff 与图片两档永远不碰 Monaco，不为它们把那个懒加载 chunk 拉下来。
  const monaco = useMonaco(previewNeedsMonaco(activeTab));

  const ports: FilePreviewPorts = React.useMemo(
    () => ({
      readFile: async (path) => {
        const view = await WorkspaceFsReadFile(sessionId, rootArg, path);
        // 归类的**产出点**就在这里：服务层把能判的失败作为结构化原因随应答带回
        // （Wails 边界只过 Error() 字符串，没有别的通道），宿主在这里把它翻成包
        // 里那套失败标记，面板才分得出「文件不存在」（终态、不给动作）与「对端
        // 离线」（可重试）。翻不过来的失败照旧原样 reject，落包里的未归类兜底。
        if (view.unavailable) {
          // 只认服务层今天真的会给的那两个取值,认不出来的就**不贴标记**——照
          // 直 reject 落包里的未归类兜底,而不是默认当成「离线」贴上重试按钮
          // (同 cwdUnavailableReason 的读法:逐个已知取值判等,其余走兜底)。
          const kind: FilePreviewFailureKind | undefined =
            view.unavailable === "not-found"
              ? "notFound"
              : view.unavailable === "offline"
                ? "offline"
                : undefined;
          if (!kind) {
            // 未归类那档由面板**如实显示 reject 自带的那句话**——那条约定的前提
            // 是宿主给得出一句已本地化的人话。这里给不出:`unavailable` 是个机器
            // token,没有译文。所以留空文案,让面板回落到它自己那句通用提示,而
            // 不是把 token 原样端到用户面前。
            throw new Error();
          }
          throw Object.assign(new Error(view.unavailable), {
            kind,
          } satisfies FilePreviewFailure);
        }
        return view;
      },
      gitFileContent: (path) =>
        WorkspaceFsGitFileContent(sessionId, rootArg, path),
    }),
    [sessionId, rootArg],
  );

  const doneTick = useSessionStatus(sessionId)?.doneTick ?? 0;

  // 关掉最后一个标签时整栏要先播 200ms 出场动画再消失：动画期间标签还留在 store
  // 里，面板照常渲染。关闭按路径下发——出场动画期间用户可能已打开另一个文件
  // （临时标签被原地替换），那一刻旧文件已经不在标签里就是 no-op，用户的新选择
  // 不会被旧 timer 清掉。
  const [closing, setClosing] = React.useState(false);
  const closeTimerRef = React.useRef<number | null>(null);
  React.useEffect(
    () => () => {
      if (closeTimerRef.current != null)
        window.clearTimeout(closeTimerRef.current);
    },
    [],
  );
  const tabCount = entry?.tabs.length ?? 0;
  const handleClose = React.useCallback(
    (path: string) => {
      if (closing) return;
      if (tabCount > 1) {
        closePreviewTab(sessionId, path);
        return;
      }
      setClosing(true);
      closeTimerRef.current = window.setTimeout(() => {
        closePreviewTab(sessionId, path);
        setClosing(false);
      }, 200);
    },
    [closePreviewTab, closing, sessionId, tabCount],
  );

  if (!activeTab) return null;

  return (
    <ResizableSidebar
      persistenceKey="file-preview"
      ariaLabel={uiT("filePreview.panelAria")}
      edge="left"
      defaultWidth={440}
      className={cn(
        "overflow-hidden",
        closing
          ? "animate-out slide-out-to-right-6 duration-200 ease-out motion-reduce:animate-none"
          : "animate-in slide-in-from-right-6 duration-200 ease-out motion-reduce:animate-none",
      )}
    >
      <SharedFilePreviewPanel
        tabs={entry?.tabs ?? []}
        activePath={activeTab.path}
        segment={activeTab.segment}
        sourceMode={activeTab.sourceMode}
        revealTarget={activeTab.reveal ?? undefined}
        ports={ports}
        // 取数目标的身份：哪个会话的哪个工作根（见包那侧 sourceKey 的注释）。
        sourceKey={`${sessionId}\n${rootArg}`}
        refreshToken={doneTick}
        monaco={monaco}
        messages={messages}
        root={root}
        renderFileIcon={renderFileIcon}
        onSegmentChange={(segment) => setPreviewSegment(sessionId, segment)}
        onActivate={(path) => activatePreviewTab(sessionId, path)}
        // 「双击转常驻」在 store 里是「先切过去，再把活动的临时标签转常驻」两步。
        onPromote={(path) => {
          activatePreviewTab(sessionId, path);
          promoteActivePreviewTab(sessionId);
        }}
        onPin={(path) => togglePreviewTabPin(sessionId, path)}
        onClose={handleClose}
        onCloseOthers={(path) => closeOtherPreviewTabs(sessionId, path)}
        onCloseAll={() => closeAllPreviewTabs(sessionId)}
      />
    </ResizableSidebar>
  );
}
