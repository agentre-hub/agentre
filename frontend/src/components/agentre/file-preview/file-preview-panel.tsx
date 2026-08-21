import * as React from "react";
import { useTranslation } from "react-i18next";

import { X } from "lucide-react";
import {
  previewKind,
  type PreviewKind,
  MarkdownText,
} from "@agentre-ai/agentre-ui";

import {
  WorkspaceFsGitFileContent,
  WorkspaceFsReadFile,
} from "@/../wailsjs/go/app/App";
import type { workspace_fs_svc } from "@/../wailsjs/go/models";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  selectActivePreviewTab,
  useChatSidebarStore,
  type FilePreviewSegment,
} from "@/stores/chat-sidebar-store";
import { useSessionStatus } from "@/stores/session-status-store";

import {
  errorText,
  PanelNotice,
  PanelSkeleton,
} from "../chat-context-sidebar/views/panel-feedback";
import { FileTypeIcon } from "../file-type-icon";
import { ResizableSidebar } from "../resizable-sidebar";

import { CodePreview } from "./code-view";
import { DiffPreview } from "./diff-view";
import { basename, dirname } from "./file-meta";
import { MarkdownSourceView } from "./markdown-source-view";
import { PreviewTabStrip } from "./preview-tab-strip";

type ReadState =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "loaded"; view: workspace_fs_svc.ReadFileView };

type GitState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "loaded"; view: workspace_fs_svc.GitFileContentView };

type Props = {
  sessionId: number;
};

/**
 * targetKey 是一次取数的目标身份：**哪个会话的哪个文件**。
 *
 * 必须带上 sessionId 而不只是 path——面板不随会话切换重挂载（chat-panel 把
 * sessionId 当普通 prop 传，没有 key），两个会话恰好都开着同一个 relPath 时，只看
 * path 的「结果已就位」闸门在切换的那一帧读起来是成立的，于是上一个工作目录的同名
 * 文件正文会真的被提交出去一帧。
 */
function targetKey(sessionId: number, path: string): string {
  return `${sessionId}\n${path}`;
}

/**
 * FilePreviewPanel 是会话「文件」面板的最右一栏预览面板（spec「状态与布局」）：
 * 仅在该会话开着预览标签时渲染，可拖拽调宽（ResizableSidebar edge="left"，独立
 * persistenceKey）。标签条（≥ 2 个标签才出现）回答「打开了哪些」，header 回答
 * 「当前这个是什么、怎么看」：markdown 三档（渲染/文本/双栏）；代码/文本无分段
 * 控件，首视图由入口模式决定（目录→内容、Git/变动→与 HEAD 对比，spec 决策 9）；
 * 图片无档。读取走 WorkspaceFsReadFile / WorkspaceFsGitFileContent（会话级
 * relPath，本机 / 远端同一绑定）；本会话轮次结束（doneTick）按 sourceMode 自动
 * 重读刷新。标签表按会话持久化，切换会话就是换一整张表（spec「多标签预览」）。
 */
export function FilePreviewPanel({ sessionId }: Props) {
  const { t } = useTranslation();
  const activeTab = useChatSidebarStore((s) =>
    selectActivePreviewTab(s, sessionId),
  );
  const tabCount = useChatSidebarStore(
    (s) => s.previewTabsBySession[sessionId]?.tabs.length ?? 0,
  );
  const path = activeTab?.path;
  const storedSegment = activeTab?.segment ?? null;
  const sourceMode = activeTab?.sourceMode;
  const setPreviewSegment = useChatSidebarStore((s) => s.setPreviewSegment);
  const closePreviewTab = useChatSidebarStore((s) => s.closePreviewTab);

  // 关闭按钮关的是当前活动标签：还有别的标签时就地切过去，关掉最后一个才播 200ms
  // 出场动画把整个面板收起来。
  const [closing, setClosing] = React.useState(false);
  const closeTimerRef = React.useRef<number | null>(null);
  React.useEffect(
    () => () => {
      if (closeTimerRef.current != null)
        window.clearTimeout(closeTimerRef.current);
    },
    [],
  );
  const handleClose = React.useCallback(() => {
    if (closing) return;
    const closedPath = path;
    if (closedPath === undefined) return;
    if (tabCount > 1) {
      closePreviewTab(sessionId, closedPath);
      return;
    }
    setClosing(true);
    closeTimerRef.current = window.setTimeout(() => {
      // 出场动画期间用户可能已打开另一个文件（临时标签被原地替换）：按路径关闭，
      // 那一刻的文件已经不在标签里就是 no-op，用户的新选择不会被旧 timer 清掉。
      closePreviewTab(sessionId, closedPath);
      setClosing(false);
    }, 200);
  }, [closing, closePreviewTab, path, sessionId, tabCount]);

  // 档位是标签自身的状态,切换标签时各自保留。markdown 的档位才是存储的
  // segment(render/text/split);代码 / 文本没有分段控件,首视图由入口模式决定
  // (showDiff)。这里把存储的档位按文件类型钳到合法集合。
  const kind: PreviewKind | null = path ? previewKind(path) : null;
  const effectiveSegment = React.useMemo(() => {
    if (kind === "image" || kind === "code") return null;
    return storedSegment === "text" || storedSegment === "split"
      ? storedSegment
      : "render";
  }, [kind, storedSegment]);
  // 代码 / 文本从 Git / 变动模式打开 → 直接展示与 git HEAD 的对比；目录模式打开
  // → 只读内容。markdown / 图片从任何模式都不对比（spec 决策 9）。
  const showDiff =
    kind === "code" && sourceMode != null && sourceMode !== "directory";

  // doneTick 每次本会话轮次结束自增一次;轮次结束是「文件可能变了」的唯一强信号,
  // 面板开着时据此重读(不读缓存,spec 决策 11)。
  const doneTick = useSessionStatus(sessionId)?.doneTick ?? 0;

  const [readState, setReadState] = React.useState<ReadState>({
    status: "loading",
  });
  const [gitState, setGitState] = React.useState<GitState>({ status: "idle" });
  const [reloadKey, setReloadKey] = React.useState(0);
  const readGenRef = React.useRef(0);
  const gitGenRef = React.useRef(0);
  // readTarget/gitTarget 记录当前 readState/gitState 对应的是**哪个会话的哪个
  // 文件**的结果。切换之后、effect 把状态重置成 loading 之前的那一帧,
  // readState/gitState 还是上一个目标的内容——若不加这道「结果必须匹配当前目标」
  // 的闸门,那一帧会把旧正文渲染在新标题之下(一帧错内容,spec 决策 12 的切文件
  // 场景)。目标的身份见 targetKey——它是会话 + 文件,不只是文件。
  const target = path === undefined ? null : targetKey(sessionId, path);
  const [readTarget, setReadTarget] = React.useState<string | null>(null);
  const [gitTarget, setGitTarget] = React.useState<string | null>(null);

  React.useEffect(() => {
    // 代际先于 return 自增:标签全关掉时此前在途的读取同样要作废。
    readGenRef.current += 1;
    const gen = readGenRef.current;
    if (!path) return;
    setReadTarget(targetKey(sessionId, path));
    setReadState({ status: "loading" });
    WorkspaceFsReadFile(sessionId, path).then(
      (view) => {
        if (readGenRef.current !== gen) return;
        setReadState({ status: "loaded", view });
      },
      (err: unknown) => {
        if (readGenRef.current !== gen) return;
        setReadState({ status: "error", message: errorText(err) });
      },
    );
  }, [sessionId, path, doneTick, reloadKey]);

  React.useEffect(() => {
    // 代际先于 return 自增:切到一个不做对比的标签(markdown / 图片 / 目录模式打
    // 开的代码文件)时,此前在途的 HEAD 读取必须作废。否则它回来时代际仍然相等,
    // gitState 会从 idle 翻成 loaded——contentKey 带着 gitState.status,正文容器
    // 因此被重挂载,用户已经滚到一半的 markdown 被拽回顶部并重播淡入。
    gitGenRef.current += 1;
    const gen = gitGenRef.current;
    if (!showDiff || !path) {
      setGitState({ status: "idle" });
      return;
    }
    setGitTarget(targetKey(sessionId, path));
    setGitState({ status: "loading" });
    WorkspaceFsGitFileContent(sessionId, path).then(
      (view) => {
        if (gitGenRef.current !== gen) return;
        setGitState({ status: "loaded", view });
      },
      (err: unknown) => {
        if (gitGenRef.current !== gen) return;
        setGitState({ status: "error", message: errorText(err) });
      },
    );
  }, [sessionId, path, showDiff, doneTick, reloadKey]);

  if (!path) return null;

  const dir = dirname(path);
  // 只有 markdown 有分段控件（渲染/文本/双栏）；代码 / 文本与图片都没有（首视图
  // 由入口模式决定，spec 决策 9）。
  const segments =
    kind === "markdown" ? (["render", "text", "split"] as const) : [];
  const SEGMENT_LABEL_KEY: Record<FilePreviewSegment, string> = {
    render: "chatContext.filePreview.segmentRender",
    text: "chatContext.filePreview.segmentText",
    split: "chatContext.filePreview.segmentSplit",
  };
  const segmentLabel = (seg: FilePreviewSegment): string =>
    t(SEGMENT_LABEL_KEY[seg]);

  const readSettled = readTarget === target;
  const gitSettled = gitTarget === target;
  const isLoading =
    readState.status === "loading" ||
    !readSettled ||
    (showDiff &&
      (gitState.status === "idle" ||
        gitState.status === "loading" ||
        !gitSettled));

  // 正文容器按内容变化重挂载 → 150ms 淡入(motion-reduce 停用);骨架屏不参与。
  const contentKey = `${path}|${effectiveSegment}|${showDiff}|${readState.status}|${gitState.status}`;

  return (
    <ResizableSidebar
      persistenceKey="file-preview"
      ariaLabel={t("chatContext.filePreview.panelAria")}
      edge="left"
      defaultWidth={440}
      className={cn(
        "overflow-hidden",
        closing
          ? "animate-out slide-out-to-right-6 duration-200 ease-out motion-reduce:animate-none"
          : "animate-in slide-in-from-right-6 duration-200 ease-out motion-reduce:animate-none",
      )}
    >
      {/*
        Esc 关闭当前活动标签（served requirement「键盘与无障碍」）。挂在包住整
        个面板内容的这一层上：标签条、header、正文里的任何位置按 Esc 都算数。
        `contents` 让这个包装层不参与布局，面板仍是 ResizableSidebar 的直接
        flex 列（ResizableSidebar 自己不转发键盘事件）。
      */}
      <div
        className="contents"
        onKeyDown={(event) => {
          if (event.key !== "Escape" || event.defaultPrevented) return;
          event.preventDefault();
          handleClose();
        }}
      >
        <PreviewTabStrip sessionId={sessionId} />
        <header
          className="flex h-10 shrink-0 items-center gap-1.5 border-b border-border pl-3 pr-2"
          data-testid="file-preview-header"
        >
          <FileTypeIcon path={path} testId="file-preview-header-icon" />
          <span
            className="shrink truncate font-mono text-xs font-semibold"
            title={path}
          >
            {basename(path)}
          </span>
          {dir !== "" ? (
            <span
              className="min-w-0 flex-1 truncate font-mono text-3xs text-muted-foreground"
              title={dir}
            >
              {dir}
            </span>
          ) : null}
          {segments.length > 1 && effectiveSegment !== null ? (
            <div
              role="group"
              aria-label={t("chatContext.filePreview.segmentGroup")}
              className="flex shrink-0 items-center rounded-md border border-border p-0.5"
            >
              {segments.map((seg) => (
                <button
                  key={seg}
                  type="button"
                  aria-pressed={effectiveSegment === seg}
                  onClick={() => setPreviewSegment(sessionId, seg)}
                  className={cn(
                    "rounded px-1.5 py-0.5 text-3xs transition-colors duration-150",
                    effectiveSegment === seg
                      ? "bg-accent font-semibold text-foreground"
                      : "text-muted-foreground hover:text-foreground",
                  )}
                >
                  {segmentLabel(seg)}
                </button>
              ))}
            </div>
          ) : null}
          <button
            type="button"
            aria-label={t("chatContext.filePreview.close")}
            title={t("chatContext.filePreview.close")}
            onClick={handleClose}
            className="ml-0.5 shrink-0 rounded-md p-1.5 text-muted-foreground transition-colors hover:text-foreground"
          >
            <X className="size-4" aria-hidden="true" />
          </button>
        </header>
        <div className="flex min-h-0 flex-1 flex-col">
          {isLoading ? (
            <PanelSkeleton label={t("chatContext.filePreview.loading")} />
          ) : (
            <div
              key={contentKey}
              className="flex min-h-0 flex-1 flex-col animate-in fade-in duration-150 motion-reduce:animate-none"
            >
              <PanelBody
                kind={kind}
                segment={effectiveSegment}
                showDiff={showDiff}
                readState={readState}
                gitState={gitState}
                path={path}
                onRetry={() => setReloadKey((k) => k + 1)}
              />
            </div>
          )}
        </div>
      </div>
    </ResizableSidebar>
  );
}

function PanelBody({
  kind,
  segment,
  showDiff,
  readState,
  gitState,
  path,
  onRetry,
}: {
  kind: PreviewKind | null;
  segment: FilePreviewSegment | null;
  showDiff: boolean;
  readState: ReadState;
  gitState: GitState;
  path: string;
  onRetry: () => void;
}) {
  const { t } = useTranslation();

  if (readState.status === "error") {
    return (
      <PreviewMessage
        text={readState.message || t("chatContext.filePreview.readFailed")}
        onRetry={onRetry}
      />
    );
  }
  if (readState.status === "loading") return null;

  const view = readState.view;
  if (view.binary) {
    return (
      <PreviewMessage
        text={t("chatContext.filePreview.binary")}
        hint={t("chatContext.filePreview.binaryHint")}
        onRetry={onRetry}
      />
    );
  }
  if (view.tooLarge) {
    return (
      <PreviewMessage
        text={t("chatContext.filePreview.tooLarge")}
        hint={
          kind === "image"
            ? t("chatContext.filePreview.tooLargeImageHint")
            : t("chatContext.filePreview.tooLargeTextHint")
        }
        onRetry={onRetry}
      />
    );
  }

  if (kind === "image") {
    return (
      <div
        className="flex min-h-0 flex-1 items-center justify-center overflow-auto p-4"
        style={{
          // 棋盘格底:衬托透明图(png)。
          backgroundImage:
            "repeating-conic-gradient(var(--muted) 0% 25%, transparent 0% 50%)",
          backgroundSize: "16px 16px",
        }}
      >
        <img
          src={`data:${view.contentType};base64,${view.content}`}
          alt={basename(path)}
          className="max-h-full max-w-full rounded-md object-contain shadow-sm"
        />
      </div>
    );
  }

  if (kind === "markdown" && segment === "split") {
    return (
      <div className="flex min-h-0 flex-1">
        <div className="min-w-0 flex-1 overflow-auto border-r border-border">
          <MarkdownSourceView
            value={view.content}
            path={path}
            ariaLabel={t("chatContext.filePreview.sourceAria", {
              name: basename(path),
            })}
            className="h-full"
          />
        </div>
        <div className="min-w-0 flex-1 overflow-auto px-4 py-3">
          <MarkdownText text={view.content} />
        </div>
      </div>
    );
  }

  if (showDiff) {
    // 代码 / 文本从 Git / 变动模式打开:左 HEAD 版本 / 右工作区,增删行底色区分。
    if (gitState.status === "error") {
      return (
        <PreviewMessage
          text={gitState.message || t("chatContext.filePreview.readFailed")}
          onRetry={onRetry}
        />
      );
    }
    if (gitState.status !== "loaded") return null;
    if (gitState.view.notARepo) {
      return (
        <PreviewMessage
          text={t("chatContext.filePreview.noGitBaseline")}
          hint={t("chatContext.filePreview.noGitBaselineHint")}
          onRetry={onRetry}
        />
      );
    }
    return (
      <>
        <div className="flex h-8 shrink-0 items-center gap-2 border-b border-border bg-muted px-3 text-3xs text-muted-foreground">
          <span className="font-mono">
            {t("chatContext.filePreview.diffHeader")}
          </span>
          <span className="ml-auto inline-flex items-center gap-3">
            <span className="inline-flex items-center gap-1">
              <span
                aria-hidden="true"
                className="size-2 rounded-[2px] bg-status-running/30"
              />
              {t("chatContext.filePreview.diffLegendAdded")}
            </span>
            <span className="inline-flex items-center gap-1">
              <span
                aria-hidden="true"
                className="size-2 rounded-[2px] bg-destructive/30"
              />
              {t("chatContext.filePreview.diffLegendDeleted")}
            </span>
          </span>
        </div>
        <DiffPreview
          original={gitState.view.content}
          modified={view.content}
          path={path}
          ariaLabel={t("chatContext.filePreview.diffAria", {
            name: basename(path),
          })}
          className="min-h-0 flex-1"
        />
      </>
    );
  }

  if (kind === "markdown" && segment === "render") {
    return (
      <div className="min-h-0 flex-1 overflow-auto px-4 py-3">
        <MarkdownText text={view.content} />
      </div>
    );
  }

  if (kind === "markdown") {
    // markdown 文本档:原始源码,Monaco 只读(markdown 语言)。
    return (
      <MarkdownSourceView
        value={view.content}
        path={path}
        ariaLabel={t("chatContext.filePreview.sourceAria", {
          name: basename(path),
        })}
        className="min-h-0 flex-1"
      />
    );
  }

  // 代码 / 文本 文本档:Monaco 只读,按扩展名语言高亮。
  return (
    <CodePreview
      value={view.content}
      path={path}
      ariaLabel={t("chatContext.filePreview.codeAria", {
        name: basename(path),
      })}
      className="min-h-0 flex-1"
    />
  );
}

/** 面板内错误 / 说明态:复用 panel-feedback 的 PanelNotice + 一个重试按钮。 */
function PreviewMessage({
  text,
  hint,
  onRetry,
}: {
  text: string;
  hint?: string;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center px-4 pb-4">
      <PanelNotice text={text} hint={hint} />
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="h-7 text-2xs"
        onClick={onRetry}
      >
        {t("chatContext.filePreview.retry")}
      </Button>
    </div>
  );
}
