import {
  Copy as CopyIcon,
  ExternalLink,
  Eye,
  FileText,
  Folder,
  Link as LinkIcon,
  PanelRight,
} from "lucide-react";
import * as React from "react";
import { toast } from "sonner";
import { useUiTranslation } from "../i18n";
import type { TFunction } from "i18next";

import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "../ui/hover-card";
import { cn } from "../lib/utils";
import { copyTextWithToast } from "../lib/clipboard-toast";
import type { PreviewAnchor } from "../file-preview/anchor";
import {
  classifyLink,
  type ClassifyLinkOptions,
  HOME_ANCHORED,
  type LinkClass,
  type LinkRoot,
} from "../lib/link-classify";
import { loopbackURLPort } from "../lib/loopback-url";
import { pathBasename } from "../lib/work-root-path";
import { previewKind } from "../lib/previewable";
import { toastOpenPathFailure } from "./open-path-failure";
import { useTranscriptPorts } from "./ports-context";
import {
  type SessionLocation,
  useItemCwd,
  useSessionLocation,
  useSessionRoots,
} from "./session-location";
import type { PreviewFileTarget, TranscriptPorts } from "./ports";

const HOVER_OPEN_DELAY_MS = 200;
const HOVER_CLOSE_DELAY_MS = 200;

/**
 * 这条链接能不能交给内置预览,以及交给宿主的是哪个目标。null = 不能。
 *
 * 归属了某个工作根(`kind.root`,只在宿主给了根时出现)就交 `{ root, path }`;
 * 否则交今天的会话级 relPath 字符串。
 *
 * 「在不在 cwd 内」只判一次:classifyLink 已经判过了,`local-internal` 的
 * `relPath` 就是它的答案,也是 popover 里显示给用户的那一条。分流处不再拿第二个
 * 函数重判一遍——`toRelPath` 的前缀比对大小写敏感、且按 cwd 的分隔符切,而
 * classifyLink 在 Windows 上是大小写不敏感、先归一分隔符再比;转录里的路径是
 * agent 自己写出来的(盘符大小写、分隔符都可能与 cwd 对不上),两处一分叉,链接就
 * 会一边显示成 cwd 内的可预览文件、一边接不上预览。
 *
 * 留给 previewable.ts 的仍是它唯一该管的那件事:扩展名 allowlist(`previewKind`)
 * ——内置预览渲染不了的类型一律不进这条路。空串 relPath(链接指向 cwd 本身)同样
 * 不是一个可预览的文件。
 */
function previewTarget(kind: LinkClass): string | PreviewFileTarget | null {
  if (kind.kind !== "local-internal") return null;
  if (kind.relPath === "" || previewKind(kind.relPath) === null) return null;
  return kind.root
    ? { root: kind.root.path, path: kind.relPath }
    : kind.relPath;
}

/**
 * 链接写了行号就把它交给宿主,没写就**一个参数都不多传**——`previewFile` 的第三
 * 参缺席是「这次不定位」的全部表达,传一个 undefined 进去会让宿主分不清「没写行
 * 号」与「写了但解析失败」。列号不参与:定位选的是整行(见 file-preview/anchor)。
 * 唯一的例外是强制预览(forcePreview):它要带第四参,第三参缺席时只能占一个
 * undefined 位,宿主同样读作「不定位」。
 */
function previewAnchor(kind: LinkClass): PreviewAnchor | undefined {
  if (kind.kind !== "local-internal" || kind.line === undefined)
    return undefined;
  return kind.endLine === undefined
    ? { line: kind.line }
    : { line: kind.line, endLine: kind.endLine };
}

type RichLinkProps = {
  href?: string;
  className?: string;
  cwd?: string;
  /** 会话 id;previewFile 分流需要它(与 readWorkspaceFile 同一种形状)。 */
  sessionId?: number;
  children: React.ReactNode;
};

// 把解析掉的后缀原样拼回去:外部打开与「复制路径」都用这一份,少一截就等于把
// 用户看到的引用改写了。文法与 link-classify 的 LINE_SUFFIX 同源(范围与列号互斥)。
function lineColSuffix(c: {
  line?: number;
  endLine?: number;
  col?: number;
}): string {
  if (c.line === undefined) return "";
  if (c.endLine !== undefined) return `:${c.line}-${c.endLine}`;
  if (c.col === undefined) return `:${c.line}`;
  return `:${c.line}:${c.col}`;
}

function fullTarget(kind: LinkClass): string {
  switch (kind.kind) {
    case "url":
      return kind.url;
    case "local-internal":
    case "local-external":
      return kind.fullPath + lineColSuffix(kind);
    case "unknown":
      return kind.href;
  }
}

function PathKindIcon({
  pathKind,
}: {
  pathKind: Extract<LinkClass, { kind: "local-internal" }>["pathKind"];
}) {
  const props = {
    "data-testid": "rich-link-path-icon",
    "data-path-kind": pathKind,
    className: "mr-1 inline-block size-3 align-text-bottom",
    "aria-hidden": true,
  } as const;
  return pathKind === "folder" ? (
    <Folder {...props} />
  ) : (
    <FileText {...props} />
  );
}

function OpenLinkIcon({
  kind,
}: {
  kind: Exclude<LinkClass["kind"], "unknown">;
}) {
  return (
    <ExternalLink
      data-testid="rich-link-open-icon"
      data-link-kind={kind}
      className="ml-1 inline-block size-3 align-text-bottom opacity-70"
      aria-hidden
    />
  );
}

/**
 * 复制走共享的复制层，不直接摸 `navigator.clipboard`：宿主可能部署在
 * `http://<局域网 IP>:port` 上（agentre-server 的控制台就是），那里
 * Clipboard API 整个对象都不存在，直接摸会抛，而这一条链接本来用
 * `execCommand` 兜底是复制得成的。回执它自己会给。
 */
async function copyToClipboard(text: string, t: TFunction) {
  await copyTextWithToast(text, {
    successTitle: t("common.copied"),
    errorTitle: t("common.copyFailed"),
  });
}

/**
 * 用户明确选了「预览」（浮窗的相反按钮、或系统打开失败提示里的出口）：不经
 * files.open_action 的握手，要宿主照样接手。能不能走这条路由调用方先判过。
 */
function forcePreview(
  kind: LinkClass,
  ports: TranscriptPorts,
  sessionId: number | undefined,
) {
  const target = previewTarget(kind);
  if (target === null || sessionId === undefined) return;
  ports.previewFile?.(sessionId, target, previewAnchor(kind), {
    force: true,
  });
}

function canPreviewLink(
  kind: LinkClass,
  ports: TranscriptPorts,
  sessionId: number | undefined,
): boolean {
  return (
    typeof ports.previewFile === "function" &&
    sessionId !== undefined &&
    previewTarget(kind) !== null
  );
}

function openWithExternalApp(
  kind: LinkClass,
  t: TFunction,
  ports: TranscriptPorts,
  sessionId: number | undefined,
  location: SessionLocation | undefined,
) {
  ports.openPath?.(fullTarget(kind))?.catch((err: unknown) => {
    // 能预览时，「本机没有这个文件」的提示附上「预览」出口。
    toastOpenPathFailure(err, t, {
      location,
      preview: canPreviewLink(kind, ports, sessionId)
        ? () => forcePreview(kind, ports, sessionId)
        : undefined,
    });
  });
}

/**
 * 外部浏览器打开 URL。带上会话号:远端会话里的 localhost 链接要经端口转发才够得到
 * 执行设备上的服务,改写由宿主做。宿主那边失败(设备离线、转发建不起来)照实说,
 * 不吞掉。
 */
function openURL(
  url: string,
  t: TFunction,
  ports: TranscriptPorts,
  sessionId: number | undefined,
) {
  const opened =
    sessionId === undefined
      ? ports.openExternalURL?.(url)
      : ports.openExternalURL?.(url, { sessionId });
  opened?.catch((err: unknown) => {
    toast.error(
      t("richLink.openFailed", {
        error: err instanceof Error ? err.message : String(err),
      }),
    );
  });
}

function openLocalInternal(
  kind: Extract<LinkClass, { kind: "local-internal" }>,
  t: TFunction,
  ports: TranscriptPorts,
  sessionId: number | undefined,
  location: SessionLocation | undefined,
) {
  // 判定见 previewTarget:判定不通过(扩展名不在内置预览的 allowlist)一律
  // 交给外部应用,与设置无关。判定通过时才去问宿主的 previewFile——设置读取
  // 留在宿主那一侧(见 ports.ts),这里只认它的布尔回执:true 表示宿主已接手,
  // 不再退回 openPath;false / 端口缺失都退回今天的外部打开路线,字节不变
  // (含 line:col 后缀)。
  const target = previewTarget(kind);
  const anchor = previewAnchor(kind);
  const tookOver =
    target !== null &&
    sessionId !== undefined &&
    (anchor === undefined
      ? (ports.previewFile?.(sessionId, target) ?? false)
      : (ports.previewFile?.(sessionId, target, anchor) ?? false));
  if (tookOver) return;
  openWithExternalApp(kind, t, ports, sessionId, location);
}

/**
 * 不在任何已认领根内的绝对路径,点击即请宿主认领(spec 决策 20:点击是明确意图)。
 * 认领后落进某个根就按根内文件处理;认领不成立(宿主 reject、或集合里仍没有包含
 * 它的根)按今天的外部文件处理。只在宿主给了根、且提供认领能力时走这条路。
 */
async function claimThenOpen(
  kind: Extract<LinkClass, { kind: "local-external" }>,
  res: ClassifyLinkOptions,
  t: TFunction,
  ports: TranscriptPorts,
  sessionId: number,
  location: SessionLocation | undefined,
) {
  const roots = await Promise.resolve(
    ports.claimPath?.(sessionId, kind.fullPath),
  ).catch(() => undefined);
  const claimed = roots
    ? classifyLink(fullTarget(kind), { ...res, roots })
    : null;
  if (claimed?.kind === "local-internal") {
    openLocalInternal(claimed, t, ports, sessionId, location);
    return;
  }
  openWithExternalApp(kind, t, ports, sessionId, location);
}

function canClaim(
  kind: Extract<LinkClass, { kind: "local-external" }>,
  res: ClassifyLinkOptions,
  ports: TranscriptPorts,
  sessionId: number | undefined,
): sessionId is number {
  return (
    res.roots !== undefined &&
    typeof ports.claimPath === "function" &&
    sessionId !== undefined &&
    !HOME_ANCHORED.test(kind.fullPath)
  );
}

function dispatchClick(
  kind: LinkClass,
  res: ClassifyLinkOptions,
  t: TFunction,
  ports: TranscriptPorts,
  sessionId: number | undefined,
  location: SessionLocation | undefined,
) {
  switch (kind.kind) {
    case "url":
      openURL(kind.url, t, ports, sessionId);
      return;
    case "local-internal":
      openLocalInternal(kind, t, ports, sessionId, location);
      return;
    case "local-external":
      if (canClaim(kind, res, ports, sessionId)) {
        void claimThenOpen(kind, res, t, ports, sessionId, location);
        return;
      }
      openWithExternalApp(kind, t, ports, sessionId, location);
      return;
    case "unknown":
      // 不拦截，让浏览器走默认行为（target=_blank fallback）。
      return;
  }
}

/** 路径拆成「目录」与「最后一段」：目录弱化、最后一段加粗。目录路径保留结尾分隔符。 */
function splitPath(path: string): { dir: string; name: string } {
  const body = path.replace(/[\\/]+$/, "");
  const cut = Math.max(body.lastIndexOf("/"), body.lastIndexOf("\\"));
  return { dir: path.slice(0, cut + 1), name: path.slice(cut + 1) };
}

/**
 * 浮窗里那一行路径。过长时从**左侧**截断（rtl 容器 + ltr 的 bdi），文件名始终
 * 完整——路径最有信息量的是尾巴。
 */
function PopoverPath({ text, split }: { text: string; split: boolean }) {
  const { dir, name } = split ? splitPath(text) : { dir: "", name: text };
  return (
    <span
      data-testid="rich-link-popover-path"
      title={text}
      className="min-w-0 flex-1 truncate text-left font-mono text-xs [direction:rtl]"
    >
      <bdi>
        {dir ? <span className="text-muted-foreground">{dir}</span> : null}
        <span className={cn(split && "font-semibold text-foreground")}>
          {name}
        </span>
      </bdi>
    </span>
  );
}

const POPOVER_BUTTON =
  "inline-flex h-6 shrink-0 items-center gap-1 rounded-md px-1.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:focus-outline";

/** 根名标签：宿主给的名字，缺省取路径最后一段。 */
function rootName(root: LinkRoot): string {
  return root.name || pathBasename(root.path);
}

function lineLabel(kind: LinkClass): string | null {
  if (kind.kind !== "local-internal" && kind.kind !== "local-external")
    return null;
  if (kind.line === undefined) return null;
  return kind.endLine === undefined
    ? `L${kind.line}`
    : `L${kind.line}-${kind.endLine}`;
}

/**
 * 浮窗只回答两件事：**这是哪个文件**、**还能怎么打开**（spec「文件链接浮窗」）。
 * 点链接本身就是默认打开方式，所以这里不再写「点击打开」；相反方式只在两条路都
 * 存在时出现（`opposite` 为 null 即整项不渲染）。
 */
function LinkPopover({
  kind,
  opposite,
  onOpposite,
  onPreviewURL,
}: {
  kind: Exclude<LinkClass, { kind: "unknown" }>;
  opposite: "preview" | "external" | null;
  onOpposite: () => void;
  /** 只有宿主提供了应用内网页预览、且这是一条 localhost 链接时才有。 */
  onPreviewURL: (() => void) | null;
}) {
  const { t } = useUiTranslation();
  const Icon =
    kind.kind === "url"
      ? LinkIcon
      : kind.pathKind === "folder"
        ? Folder
        : FileText;
  const text =
    kind.kind === "url"
      ? kind.url
      : kind.kind === "local-internal" && kind.relPath !== ""
        ? kind.relPath
        : kind.fullPath;
  const line = lineLabel(kind);
  // 归属的根不是会话主根时，路径前标出根名：同一个 src/a.ts 在不同 checkout 里
  // 是不同的文件。
  const root =
    kind.kind === "local-internal" && kind.root && !kind.root.primary
      ? kind.root
      : null;
  const copyLabel =
    kind.kind === "url" ? t("richLink.copyLink") : t("richLink.copyPath");
  return (
    <div
      data-testid="rich-link-popover"
      className="flex min-w-0 items-center gap-1.5"
    >
      <Icon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
      {root ? (
        <span
          data-testid="rich-link-popover-root"
          title={t("richLink.workRootTitle", { path: root.path })}
          className="max-w-32 shrink-0 truncate rounded bg-secondary px-1 font-mono text-3xs text-muted-foreground"
        >
          {rootName(root)}
        </span>
      ) : null}
      <PopoverPath text={text} split={kind.kind !== "url"} />
      {line ? (
        <span className="shrink-0 rounded bg-secondary px-1 font-mono text-3xs text-muted-foreground">
          {line}
        </span>
      ) : null}
      {opposite === "external" ? (
        <button
          type="button"
          aria-label={t("richLink.openExternalAria")}
          title={t("richLink.openExternalAria")}
          className={POPOVER_BUTTON}
          onClick={onOpposite}
        >
          <ExternalLink className="size-3.5" aria-hidden />
          {t("richLink.openExternal")}
        </button>
      ) : opposite === "preview" ? (
        <button
          type="button"
          aria-label={t("richLink.openPreviewAria")}
          title={t("richLink.openPreviewAria")}
          className={POPOVER_BUTTON}
          onClick={onOpposite}
        >
          <Eye className="size-3.5" aria-hidden />
          {t("richLink.openPreview")}
        </button>
      ) : null}
      {onPreviewURL ? (
        <button
          type="button"
          aria-label={t("richLink.previewInApp")}
          title={t("richLink.previewInApp")}
          className={POPOVER_BUTTON}
          onClick={onPreviewURL}
        >
          <PanelRight className="size-3.5" aria-hidden />
          {t("richLink.previewInApp")}
        </button>
      ) : null}
      <button
        type="button"
        aria-label={copyLabel}
        title={copyLabel}
        className={cn(POPOVER_BUTTON, "w-6 justify-center px-0")}
        onClick={() => copyToClipboard(fullTarget(kind), t)}
      >
        <CopyIcon className="size-3.5" aria-hidden />
      </button>
    </div>
  );
}

export function RichLink({
  href,
  className,
  cwd,
  sessionId,
  children,
}: RichLinkProps) {
  const { t } = useUiTranslation();
  const ports = useTranscriptPorts();
  const location = useSessionLocation();
  const roots = useSessionRoots();
  const itemCwd = useItemCwd();
  // 链接怎么解析:宿主给了工作根(桌面端)时按「AI 当时所在目录」补全、按最深的根
  // 归属;没给(agentre-server)时只按会话工作目录 —— 今天的行为,一个字节不变。
  // 预览、复制路径、系统打开都从这一份解析结果出发。
  const res = React.useMemo<ClassifyLinkOptions>(
    () => (roots ? { cwd: itemCwd ?? cwd, roots } : { cwd }),
    [roots, itemCwd, cwd],
  );
  const kind = React.useMemo(() => classifyLink(href, res), [href, res]);
  const [open, setOpen] = React.useState(false);

  if (kind.kind === "unknown") {
    // unknown 一律走原有 anchor 行为（target=_blank 兜底，不挂 popover）
    return (
      <a
        href={kind.href || undefined}
        className={cn(
          "text-primary-text underline underline-offset-2 hover:opacity-80",
          className,
        )}
        target="_blank"
        rel="noreferrer noopener"
      >
        {children}
      </a>
    );
  }

  // 「入口不渲染」而不是「渲染出来点了没反应」(spec「入口与可用性」)。判据是
  // 「这条路径在这台宿主上到底有没有一个去处」,而不是「它可不可预览」:
  //   - 内置预览:要 previewFile,且这条链接有一个可预览的目标(见
  //     previewTarget —— 与点下去真正分流用的是同一个判定,不是另判一遍);
  //   - 外部应用:要 openPath —— 桌面端的退路,浏览器里根本没有这个去向。
  // 两条都不成立时它就不该看起来能点。控制台里 allowlist 外的路径(「它没有
  // 『交给外部应用』这条退路」)与越出 cwd 的路径都落在这里。这条判定只看端口
  // 能力,不看 files.open_action(设置只决定两条路都在时走哪条,与「有没有路」无关)。
  const canPreview = canPreviewLink(kind, ports, sessionId);
  if (
    (kind.kind === "local-internal" || kind.kind === "local-external") &&
    !canPreview &&
    typeof ports.openPath !== "function"
  ) {
    return <span className={className}>{children}</span>;
  }

  const onClick = (e: React.MouseEvent) => {
    e.preventDefault();
    dispatchClick(kind, res, t, ports, sessionId, location);
  };

  // 相反打开方式：两条路都在（能预览 + 宿主有系统打开）且宿主告知了默认方式。
  const openDefault =
    canPreview && typeof ports.openPath === "function"
      ? ports.fileOpenDefault?.()
      : undefined;
  const opposite =
    openDefault === "preview"
      ? "external"
      : openDefault === "external"
        ? "preview"
        : null;
  const onOpposite = () => {
    setOpen(false);
    if (opposite === "external")
      openWithExternalApp(kind, t, ports, sessionId, location);
    else forcePreview(kind, ports, sessionId);
  };

  const onPreviewURL =
    kind.kind === "url" &&
    sessionId !== undefined &&
    ports.previewURL !== undefined &&
    loopbackURLPort(kind.url) !== null
      ? () => {
          setOpen(false);
          ports.previewURL?.(sessionId, kind.url);
        }
      : null;

  return (
    <HoverCard
      open={open}
      onOpenChange={setOpen}
      openDelay={HOVER_OPEN_DELAY_MS}
      closeDelay={HOVER_CLOSE_DELAY_MS}
    >
      <HoverCardTrigger asChild>
        <a
          href={fullTarget(kind)}
          className={cn(
            // 保持普通 inline 排版:一旦是 flex 容器,标签文本会变成 flex item,
            // 其自动最小尺寸等于 min-content(整条不可断的路径),继承的
            // overflow-wrap:break-word 不作数,长路径会把链接盒子撑出消息列。
            // 图标间距因此用外边距,不用 flex gap。
            "text-primary-text underline underline-offset-2 hover:opacity-80",
            className,
          )}
          onClick={onClick}
          rel="noreferrer noopener"
        >
          {kind.kind === "local-internal" || kind.kind === "local-external" ? (
            <PathKindIcon pathKind={kind.pathKind} />
          ) : null}
          {children}
          <OpenLinkIcon kind={kind.kind} />
        </a>
      </HoverCardTrigger>
      <HoverCardContent className="w-auto min-w-64 max-w-[min(28rem,calc(100vw-2rem))] p-1.5">
        <LinkPopover
          kind={kind}
          opposite={opposite}
          onOpposite={onOpposite}
          onPreviewURL={onPreviewURL}
        />
      </HoverCardContent>
    </HoverCard>
  );
}
