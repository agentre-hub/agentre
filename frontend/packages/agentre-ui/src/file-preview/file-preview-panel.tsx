import * as React from "react";

import { X } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { previewKind, type PreviewKind } from "../lib/previewable";
import { cn } from "../lib/utils";
import { MarkdownText } from "../transcript/markdown-text";
import { collectReplayCalls } from "../transcript/canonical-tool/file-edit/replay-calls";
import { replayPatches } from "../transcript/canonical-tool/file-edit/replay";
import type { ReplayResult } from "../transcript/canonical-tool/file-edit/replay";
import { ReplayedFileDiff } from "../transcript/canonical-tool/file-edit/replay-view";
import type { TranscriptMessage } from "../transcript/dto";
import { Button } from "../ui/button";
import { CodePreview } from "./code-view";
import { DiffPreview } from "./diff-view";
import { basename, dirname } from "./file-meta";
import { ImagePreview } from "./image-view";
import { MarkdownSourceView } from "./markdown-source-view";
import type { MonacoNS } from "./monaco";
import {
  filePreviewFailureKind,
  filePreviewFailureText,
  type FilePreviewFailureKind,
  type FilePreviewPorts,
  type GitFileContentResult,
  type ReadFileResult,
} from "./ports";
import { PreviewNotice, PreviewSkeleton } from "./preview-feedback";
import {
  PreviewTabStrip,
  type FilePreviewTab,
  type PreviewTabStripProps,
} from "./preview-tab-strip";

/** markdown 的视图档位；代码 / 文本与图片没有档位控件。 */
export type FilePreviewSegment = "render" | "text" | "split";

/**
 * 这个标签是从哪儿点开的 —— 它决定首视图（spec 决策 9）：
 * `directory` 只读内容，`git` 直接与 HEAD 对比，`session` 画本会话的工具 diff。
 */
export type FilePreviewSourceMode = "directory" | "git" | "session";

/**
 * 文件身份图标由宿主注入（图标目录是宿主自己的资产，两端各有一套）。
 * `header` 是面板标题那一枚，`tab` / `overflow` 是标签条那两处。
 */
export type FilePreviewIconRenderer = (
  path: string,
  slot: "tab" | "overflow" | "header",
) => React.ReactNode;

export type FilePreviewPanelProps = {
  /** 本会话开着的全部预览标签（标签条 ≥ 2 个才渲染）。 */
  tabs: FilePreviewTab[];
  /**
   * 当前活动标签的路径；为 null 时整个面板不渲染。
   *
   * 「它在 tabs 里」是**宿主的不变量**（两端的 store 都从同一张表里取活动标签），
   * 面板不复查：`tabs` 与 `activePath` 常常来自两次分开的 state 提交，中间那一帧
   * 查出「不在里面」就会把整栏卸载掉——Monaco 编辑器与用户滚到的位置一并没了，
   * 比多画一帧标题糟得多。
   */
  activePath: string | null;
  /** 活动标签存着的 markdown 档位。非 markdown 的标签传 null。 */
  segment: FilePreviewSegment | null;
  /** 活动标签的来源模式。 */
  sourceMode: FilePreviewSourceMode;
  /** 取数端口（见 ./ports）。 */
  ports: FilePreviewPorts;
  /**
   * 取数目标的**身份**：哪个会话的哪个工作根。它变了就是换了一个目标，此前在途
   * 的结果一律作废。
   *
   * 为什么不能只看 path：面板通常不随会话切换重挂载，两个会话恰好都开着同一条
   * relPath 时，只看 path 的「结果已就位」闸门在切换的那一帧读起来是成立的，于是
   * 上一个工作目录的同名文件正文会真的被提交出去一帧。多工作根同理。
   */
  sourceKey: string;
  /**
   * 变一次就重读一次。桌面端接的是「本会话轮次结束」（doneTick）—— 轮次结束是
   * 「文件可能变了」的唯一强信号。
   */
  refreshToken?: number;
  /**
   * 宿主注入的 Monaco 命名空间（装载器留在宿主，见 ./monaco）。还没装载好时是
   * null：内容容器留空。用 `previewNeedsMonaco` 决定要不要装载。
   */
  monaco?: MonacoNS | null;
  /**
   * 本会话的消息。只有 `session` 档用它：那一档的内容全部来自消息里的 canonical
   * 块，一次取数都不打（spec「与『有没有提交』无关」）。
   */
  messages?: TranscriptMessage[];
  /** 工作根绝对路径：`session` 档判定工具调用的路径归属时用，与「变更」行同源。 */
  root?: string;
  onSegmentChange: (segment: FilePreviewSegment) => void;
  onActivate: PreviewTabStripProps["onActivate"];
  onPromote: PreviewTabStripProps["onPromote"];
  onPin: PreviewTabStripProps["onPin"];
  onClose: (path: string) => void;
  onCloseOthers: PreviewTabStripProps["onCloseOthers"];
  onCloseAll: PreviewTabStripProps["onCloseAll"];
  renderFileIcon?: FilePreviewIconRenderer;
  className?: string;
};

type ReadState =
  | { status: "loading" }
  | { status: "error"; kind: FilePreviewFailureKind | null; message: string }
  | { status: "loaded"; view: ReadFileResult };

type GitState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "error"; kind: FilePreviewFailureKind | null; message: string }
  | { status: "loaded"; view: GitFileContentResult };

/** 稳定的空数组：默认值写成行内 `[]` 会让重放的 memo 每次渲染都重算。 */
const NO_MESSAGES: TranscriptMessage[] = [];

/**
 * 这个标签的内容要不要 Monaco。宿主据此决定装不装载那个几 MB 的 chunk ——
 * 工具 diff（画的是重放出来的行 diff）与图片（就是个 `<img>`）两档永远不碰它。
 *
 * 判据留在包里是因为「哪一档用 Monaco」本来就是面板的渲染决定；宿主只负责装载。
 */
export function previewNeedsMonaco(
  tab: { path: string; sourceMode: FilePreviewSourceMode } | null | undefined,
): boolean {
  if (!tab) return false;
  return tab.sourceMode !== "session" && previewKind(tab.path) !== "image";
}

/** 一次取数的目标身份：宿主给的身份 + 文件。 */
function targetKey(sourceKey: string, path: string): string {
  return `${sourceKey}\n${path}`;
}

/**
 * FilePreviewPanel 是会话「文件」预览面板：仅在该会话开着预览标签时渲染（没有
 * 活动标签时整个返回 null）。**它不带容器** —— 开在哪、多宽、能不能拖、收起时
 * 怎么动画，全是宿主的布局问题（spec 决策 3：「开在哪、开几个、怎么排」是宿主
 * 的布局问题）：桌面端把它放进右侧栏那条可拖的 ResizableSidebar，控制台放进详情
 * 列里那条定宽 420、不可拖的分栏。
 *
 * 标签条（≥ 2 个标签才出现）回答「打开了哪些」，header 回答
 * 「当前这个是什么、怎么看」：markdown 三档（渲染/文本/双栏）；代码/文本无分段
 * 控件，首视图由入口模式决定（目录→内容、「未提交」→与 HEAD 对比，spec 决策 9）；
 * 图片无档。
 *
 * 读取走注入的 `ports`（会话级 relPath，本机 / 远端同一形状），`refreshToken` 变
 * 一次重读一次。开着哪些标签、谁是活动标签、动作落到哪个 store，一概由宿主经
 * props 注入（桌面端是 file-preview-tabs-store，控制台是它自己的那份）。
 *
 * 失败按 spec「失败与恢复」分档：`tooLarge` / `binary` / 文件不存在是**终态**，
 * 各出各自的说明且不给动作（文件不会自己变小、二进制不会自己变成文本、删掉的
 * 文件不会自己回来）；只有对端离线 / 中继断开那一态给重试。归类不到的失败如实
 * 显示它自己的文案 + 重试。
 *
 * 「本次会话」档（sourceMode = "session"）是唯一不走端口的入口：它画的是**工具
 * diff** —— 该文件本会话每一次工具调用重放出的那一个连续 diff（spec 决策 4），
 * 与该文件此后有没有被提交无关，因此不读文件、不读 git，也没有档位控件。
 */
export function FilePreviewPanel({
  tabs,
  activePath,
  segment: storedSegment,
  sourceMode,
  ports,
  sourceKey,
  refreshToken = 0,
  monaco = null,
  messages = NO_MESSAGES,
  root = "",
  onSegmentChange,
  onActivate,
  onPromote,
  onPin,
  onClose,
  onCloseOthers,
  onCloseAll,
  renderFileIcon,
  className,
}: FilePreviewPanelProps) {
  const { t } = useUiTranslation();
  const path = activePath ?? undefined;

  // 端口经 ref 使用：宿主每次渲染都新建一个 ports 对象是完全正常的（它是几个
  // 闭包），把它写进 effect 依赖会让「读一次」变成「每渲染读一次」。取数目标的
  // 身份由 sourceKey 回答，那才是真正决定「要不要重读」的东西。
  const portsRef = React.useRef(ports);
  React.useEffect(() => {
    portsRef.current = ports;
  });

  // 关闭按钮（与 Esc）关的是当前活动标签。关掉最后一个之后整栏怎么收起（要不要
  // 播出场动画、播多久）是宿主的布局问题（spec 决策 3），面板只把「用户要关这个
  // 文件」报给宿主。
  const handleClose = React.useCallback(() => {
    if (path === undefined) return;
    onClose(path);
  }, [onClose, path]);

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
  // 从「本次会话」点开的一行看的是**工具 diff**：把该文件本会话每一次工具调用
  // 按调用先后重放成一个连续 diff（spec 决策 4）。这一档不读文件也不读 git ——
  // 内容全部来自消息里的 canonical 块，因此 AI 中途提交、rebase 或 amend 都不
  // 影响它；文件类型也不参与（markdown / 图片同样看这份 diff，档位控件随之隐藏）。
  const isToolDiff = sourceMode === "session";
  // 代码 / 文本从「未提交」档打开 → 直接展示与 git HEAD 的对比；目录模式打开
  // → 只读内容。markdown / 图片从任何模式都不对比（spec 决策 9）。
  const showDiff = kind === "code" && sourceMode === "git";

  // 归属判定与「变更」行同源（collectReplayCalls 里做一次路径解析）：同一个文件
  // 被绝对与相对两种写法改过时行只有一条，这份 diff 也必须两次都算进去。
  const replayCalls = React.useMemo(
    () => (isToolDiff && path ? collectReplayCalls(messages, root, path) : []),
    [isToolDiff, messages, root, path],
  );
  // 一次调用都没有 ≠ 改动互相抵消：前者是「这一档没有这个文件」，画一个空 diff
  // 会把它说成后者。toolDiff 为 null 时由 PanelBody 出那句专门的说明。
  const toolDiff: ReplayResult | null = React.useMemo(
    () => (replayCalls.length > 0 ? replayPatches(replayCalls) : null),
    [replayCalls],
  );

  const [readState, setReadState] = React.useState<ReadState>({
    status: "loading",
  });
  const [gitState, setGitState] = React.useState<GitState>({ status: "idle" });
  const [reloadKey, setReloadKey] = React.useState(0);
  const readGenRef = React.useRef(0);
  const gitGenRef = React.useRef(0);
  // readTarget/gitTarget 记录当前 readState/gitState 对应的是**哪个目标**的结果。
  // 切换之后、effect 把状态重置成 loading 之前的那一帧,readState/gitState 还是上
  // 一个目标的内容——若不加这道「结果必须匹配当前目标」的闸门,那一帧会把旧正文
  // 渲染在新标题之下(一帧错内容,spec 决策 12 的切文件场景)。
  const target = path === undefined ? null : targetKey(sourceKey, path);
  const [readTarget, setReadTarget] = React.useState<string | null>(null);
  const [gitTarget, setGitTarget] = React.useState<string | null>(null);

  React.useEffect(() => {
    // 代际先于 return 自增:标签全关掉时此前在途的读取同样要作废。
    readGenRef.current += 1;
    const gen = readGenRef.current;
    // 工具 diff 不看工作区里现在是什么样：这一档一次取数都不打。
    if (!path || isToolDiff) return;
    setReadTarget(targetKey(sourceKey, path));
    setReadState({ status: "loading" });
    portsRef.current.readFile(path).then(
      (view) => {
        if (readGenRef.current !== gen) return;
        setReadState({ status: "loaded", view });
      },
      (err: unknown) => {
        if (readGenRef.current !== gen) return;
        setReadState({
          status: "error",
          kind: filePreviewFailureKind(err),
          message: filePreviewFailureText(err),
        });
      },
    );
  }, [sourceKey, path, isToolDiff, refreshToken, reloadKey]);

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
    setGitTarget(targetKey(sourceKey, path));
    setGitState({ status: "loading" });
    portsRef.current.gitFileContent(path).then(
      (view) => {
        if (gitGenRef.current !== gen) return;
        setGitState({ status: "loaded", view });
      },
      (err: unknown) => {
        if (gitGenRef.current !== gen) return;
        setGitState({
          status: "error",
          kind: filePreviewFailureKind(err),
          message: filePreviewFailureText(err),
        });
      },
    );
  }, [sourceKey, path, showDiff, refreshToken, reloadKey]);

  if (!path) return null;

  const dir = dirname(path);
  // 只有 markdown 有分段控件（渲染/文本/双栏）；代码 / 文本与图片都没有（首视图
  // 由入口模式决定，spec 决策 9）。
  const segments =
    kind === "markdown" && !isToolDiff
      ? (["render", "text", "split"] as const)
      : [];
  const SEGMENT_LABEL_KEY: Record<FilePreviewSegment, string> = {
    render: "filePreview.segmentRender",
    text: "filePreview.segmentText",
    split: "filePreview.segmentSplit",
  };
  const segmentLabel = (seg: FilePreviewSegment): string =>
    t(SEGMENT_LABEL_KEY[seg]);

  const readSettled = readTarget === target;
  const gitSettled = gitTarget === target;
  // 工具 diff 是同步算出来的，没有在途取数可等 —— 骨架屏在这一档下永远不该出现，
  // 否则上一个标签留下的 readTarget 会把它按在加载态上。
  const isLoading =
    !isToolDiff &&
    (readState.status === "loading" ||
      !readSettled ||
      (showDiff &&
        (gitState.status === "idle" ||
          gitState.status === "loading" ||
          !gitSettled)));

  // 正文容器按内容变化重挂载 → 150ms 淡入(motion-reduce 停用);骨架屏不参与。
  const contentKey = `${path}|${effectiveSegment}|${showDiff}|${isToolDiff}|${readState.status}|${gitState.status}`;

  return (
    /*
      Esc 关闭当前活动标签（served requirement「键盘与无障碍」）：挂在面板根上，
      标签条、header、正文里的任何位置按 Esc 都算数。根本身是一条撑满宿主容器的
      flex 列 —— 宽度、边框、拖拽手柄与出场动画都由宿主那一层给。
    */
    <div
      data-testid="file-preview-panel"
      className={cn("flex min-h-0 flex-1 flex-col overflow-hidden", className)}
      onKeyDown={(event) => {
        if (event.key !== "Escape" || event.defaultPrevented) return;
        event.preventDefault();
        handleClose();
      }}
    >
      <PreviewTabStrip
        tabs={tabs}
        activePath={activePath}
        onActivate={onActivate}
        onPromote={onPromote}
        onPin={onPin}
        onClose={onClose}
        onCloseOthers={onCloseOthers}
        onCloseAll={onCloseAll}
        renderFileIcon={renderFileIcon}
      />
      <header
        className="flex h-10 shrink-0 items-center gap-1.5 border-b border-border pl-3 pr-2"
        data-testid="file-preview-header"
      >
        {renderFileIcon?.(path, "header")}
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
            aria-label={t("filePreview.segmentGroup")}
            className="flex shrink-0 items-center rounded-md border border-border p-0.5"
          >
            {segments.map((seg) => (
              <button
                key={seg}
                type="button"
                aria-pressed={effectiveSegment === seg}
                onClick={() => onSegmentChange(seg)}
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
          aria-label={t("filePreview.close")}
          title={t("filePreview.close")}
          onClick={handleClose}
          className="ml-0.5 shrink-0 rounded-md p-1.5 text-muted-foreground transition-colors hover:text-foreground"
        >
          <X className="size-4" aria-hidden="true" />
        </button>
      </header>
      <div className="flex min-h-0 flex-1 flex-col">
        {isLoading ? (
          <PreviewSkeleton label={t("filePreview.loading")} />
        ) : (
          <div
            key={contentKey}
            className="flex min-h-0 flex-1 flex-col animate-in fade-in duration-150 motion-reduce:animate-none"
          >
            <PanelBody
              monaco={monaco}
              kind={kind}
              segment={effectiveSegment}
              showDiff={showDiff}
              toolDiff={toolDiff}
              isToolDiff={isToolDiff}
              readState={readState}
              gitState={gitState}
              path={path}
              onRetry={() => setReloadKey((k) => k + 1)}
            />
          </div>
        )}
      </div>
    </div>
  );
}

function PanelBody({
  monaco,
  kind,
  segment,
  showDiff,
  toolDiff,
  isToolDiff,
  readState,
  gitState,
  path,
  onRetry,
}: {
  monaco: MonacoNS | null;
  kind: PreviewKind | null;
  segment: FilePreviewSegment | null;
  showDiff: boolean;
  toolDiff: ReplayResult | null;
  isToolDiff: boolean;
  readState: ReadState;
  gitState: GitState;
  path: string;
  onRetry: () => void;
}) {
  const { t } = useUiTranslation();

  if (isToolDiff) {
    // 本会话没有一次工具调用动过这个文件（例如调用落在工作根子树之外，或消息
    // 还没加载完）：说出来，而不是画一个看起来「没有差异」的空 diff。
    if (!toolDiff) {
      return (
        <PreviewNotice
          text={t("filePreview.noToolChanges")}
          hint={t("filePreview.noToolChangesHint")}
        />
      );
    }
    return (
      <>
        <div className="flex h-8 shrink-0 items-center gap-2 border-b border-border bg-muted px-3 text-3xs text-muted-foreground">
          <span className="font-mono">{t("filePreview.toolDiffHeader")}</span>
        </div>
        <div className="min-h-0 flex-1 overflow-auto text-xs">
          <ReplayedFileDiff path={path} result={toolDiff} />
        </div>
      </>
    );
  }

  if (readState.status === "error") {
    return (
      <ReadFailure
        kind={readState.kind}
        message={readState.message}
        onRetry={onRetry}
      />
    );
  }
  if (readState.status === "loading") return null;

  const view = readState.view;
  // 二进制与过大都是**终态**：文件不会自己变小、二进制不会自己变成文本。给一个
  // 点下去必然还是这句话的按钮，比不给更糟（spec「失败与恢复」）。
  if (view.binary) {
    return (
      <PreviewNotice
        text={t("filePreview.binary")}
        hint={t("filePreview.binaryHint")}
      />
    );
  }
  if (view.tooLarge) {
    return (
      <PreviewNotice
        text={t("filePreview.tooLarge")}
        hint={
          kind === "image"
            ? t("filePreview.tooLargeImageHint")
            : t("filePreview.tooLargeTextHint")
        }
      />
    );
  }

  if (kind === "image") {
    return (
      <ImagePreview
        content={view.content}
        contentType={view.contentType}
        alt={basename(path)}
      />
    );
  }

  if (kind === "markdown" && segment === "split") {
    return (
      <div className="flex min-h-0 flex-1">
        <div className="min-w-0 flex-1 overflow-auto border-r border-border">
          <MarkdownSourceView
            value={view.content}
            path={path}
            monaco={monaco}
            ariaLabel={t("filePreview.sourceAria", { name: basename(path) })}
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
        <ReadFailure
          kind={gitState.kind}
          message={gitState.message}
          onRetry={onRetry}
        />
      );
    }
    if (gitState.status !== "loaded") return null;
    if (gitState.view.notARepo) {
      return (
        <PreviewMessage
          text={t("filePreview.noGitBaseline")}
          hint={t("filePreview.noGitBaselineHint")}
          onRetry={onRetry}
        />
      );
    }
    return (
      <>
        <div className="flex h-8 shrink-0 items-center gap-2 border-b border-border bg-muted px-3 text-3xs text-muted-foreground">
          <span className="font-mono">{t("filePreview.diffHeader")}</span>
          <span className="ml-auto inline-flex items-center gap-3">
            <span className="inline-flex items-center gap-1">
              <span
                aria-hidden="true"
                className="size-2 rounded-[2px] bg-status-running/30"
              />
              {t("filePreview.diffLegendAdded")}
            </span>
            <span className="inline-flex items-center gap-1">
              <span
                aria-hidden="true"
                className="size-2 rounded-[2px] bg-destructive/30"
              />
              {t("filePreview.diffLegendDeleted")}
            </span>
          </span>
        </div>
        <DiffPreview
          original={gitState.view.content}
          modified={view.content}
          path={path}
          monaco={monaco}
          ariaLabel={t("filePreview.diffAria", { name: basename(path) })}
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
        monaco={monaco}
        ariaLabel={t("filePreview.sourceAria", { name: basename(path) })}
        className="min-h-0 flex-1"
      />
    );
  }

  // 代码 / 文本 文本档:Monaco 只读,按扩展名语言高亮。
  return (
    <CodePreview
      value={view.content}
      path={path}
      monaco={monaco}
      ariaLabel={t("filePreview.codeAria", { name: basename(path) })}
      className="min-h-0 flex-1"
    />
  );
}

/**
 * 取数失败的三种去向（spec「失败与恢复」）：
 *   - 文件不存在：终态，不给动作 —— 删掉的文件不会自己回来。
 *   - 对端离线 / 中继断开：那台机器过会儿可能就回来了，给重试。
 *   - 归类不到的失败：如实显示宿主给的那句话，同样给重试。
 */
function ReadFailure({
  kind,
  message,
  onRetry,
}: {
  kind: FilePreviewFailureKind | null;
  message: string;
  onRetry: () => void;
}) {
  const { t } = useUiTranslation();

  if (kind === "notFound") {
    return (
      <PreviewNotice
        text={t("filePreview.notFound")}
        hint={t("filePreview.notFoundHint")}
      />
    );
  }
  return (
    <PreviewMessage
      text={
        kind === "offline"
          ? t("filePreview.offline")
          : message || t("filePreview.readFailed")
      }
      onRetry={onRetry}
    />
  );
}

/** 面板内可重试的说明态：一段说明 + 一个重试按钮。 */
function PreviewMessage({
  text,
  hint,
  onRetry,
}: {
  text: string;
  hint?: string;
  onRetry: () => void;
}) {
  const { t } = useUiTranslation();
  return (
    <div className="flex flex-col items-center px-4 pb-4">
      <PreviewNotice text={text} hint={hint} />
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="h-7 text-2xs"
        onClick={onRetry}
      >
        {t("filePreview.retry")}
      </Button>
    </div>
  );
}
