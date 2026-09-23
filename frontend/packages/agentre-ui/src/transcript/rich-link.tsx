import {
  Copy as CopyIcon,
  ExternalLink,
  Eye,
  FileText,
  Folder,
  Link as LinkIcon,
} from "lucide-react";
import * as React from "react";
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
import { classifyLink, type LinkClass } from "../lib/link-classify";
import { previewKind } from "../lib/previewable";
import { toastOpenPathFailure } from "./open-path-failure";
import { useTranscriptPorts } from "./ports-context";
import type { TranscriptPorts } from "./ports";

const HOVER_OPEN_DELAY_MS = 200;
const HOVER_CLOSE_DELAY_MS = 200;

/**
 * 这条链接能不能交给内置预览,以及交的是哪条会话级 relPath。null = 不能。
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
function previewRelPath(kind: LinkClass): string | null {
  if (kind.kind !== "local-internal") return null;
  if (kind.relPath === "" || previewKind(kind.relPath) === null) return null;
  return kind.relPath;
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
  const relPath = previewRelPath(kind);
  if (relPath === null || sessionId === undefined) return;
  ports.previewFile?.(sessionId, relPath, previewAnchor(kind), {
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
    previewRelPath(kind) !== null
  );
}

function openWithExternalApp(
  kind: LinkClass,
  t: TFunction,
  ports: TranscriptPorts,
  sessionId: number | undefined,
) {
  ports.openPath?.(fullTarget(kind))?.catch((err: unknown) => {
    // 能预览时，「本机没有这个文件」的提示附上「预览」出口。
    toastOpenPathFailure(
      err,
      t,
      canPreviewLink(kind, ports, sessionId)
        ? () => forcePreview(kind, ports, sessionId)
        : undefined,
    );
  });
}

function dispatchClick(
  kind: LinkClass,
  t: TFunction,
  ports: TranscriptPorts,
  sessionId: number | undefined,
) {
  switch (kind.kind) {
    case "url":
      ports.openExternalURL?.(kind.url);
      return;
    case "local-internal": {
      // 判定见 previewRelPath:判定不通过(扩展名不在内置预览的 allowlist)一律
      // 交给外部应用,与设置无关。判定通过时才去问宿主的 previewFile——设置读取
      // 留在宿主那一侧(见 ports.ts),这里只认它的布尔回执:true 表示宿主已接手,
      // 不再退回 openPath;false / 端口缺失都退回今天的外部打开路线,字节不变
      // (含 line:col 后缀)。
      const relPath = previewRelPath(kind);
      const anchor = previewAnchor(kind);
      const tookOver =
        relPath !== null &&
        sessionId !== undefined &&
        (anchor === undefined
          ? (ports.previewFile?.(sessionId, relPath) ?? false)
          : (ports.previewFile?.(sessionId, relPath, anchor) ?? false));
      if (tookOver) return;
      openWithExternalApp(kind, t, ports, sessionId);
      return;
    }
    case "local-external":
      openWithExternalApp(kind, t, ports, sessionId);
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
  "inline-flex h-6 shrink-0 items-center gap-1 rounded-md px-1.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring";

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
}: {
  kind: Exclude<LinkClass, { kind: "unknown" }>;
  opposite: "preview" | "external" | null;
  onOpposite: () => void;
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
  const copyLabel =
    kind.kind === "url" ? t("richLink.copyLink") : t("richLink.copyPath");
  return (
    <div
      data-testid="rich-link-popover"
      className="flex min-w-0 items-center gap-1.5"
    >
      <Icon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
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
  const kind = React.useMemo(() => classifyLink(href, cwd), [href, cwd]);
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
  //   - 内置预览:要 previewFile,且这条链接有一条可预览的 relPath(见
  //     previewRelPath —— 与点下去真正分流用的是同一个判定,不是另判一遍);
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
    dispatchClick(kind, t, ports, sessionId);
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
    if (opposite === "external") openWithExternalApp(kind, t, ports, sessionId);
    else forcePreview(kind, ports, sessionId);
  };

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
        <LinkPopover kind={kind} opposite={opposite} onOpposite={onOpposite} />
      </HoverCardContent>
    </HoverCard>
  );
}
