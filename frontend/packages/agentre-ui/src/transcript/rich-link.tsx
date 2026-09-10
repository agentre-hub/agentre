import {
  Copy as CopyIcon,
  ExternalLink,
  FileText,
  Folder,
  Link as LinkIcon,
  MousePointerClick,
} from "lucide-react";
import * as React from "react";
import { useUiTranslation } from "../i18n";
import type { TFunction } from "i18next";
import { toast } from "sonner";

import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "../ui/hover-card";
import { cn } from "../lib/utils";
import { copyTextWithToast } from "../lib/clipboard-toast";
import { classifyLink, type LinkClass } from "../lib/link-classify";
import { previewKind } from "../lib/previewable";
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

type RichLinkProps = {
  href?: string;
  className?: string;
  cwd?: string;
  /** 会话 id;previewFile 分流需要它(与 readWorkspaceFile 同一种形状)。 */
  sessionId?: number;
  children: React.ReactNode;
};

function lineColSuffix(c: { line?: number; col?: number }): string {
  if (c.line === undefined) return "";
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

function openWithExternalApp(
  kind: LinkClass,
  t: TFunction,
  ports: TranscriptPorts,
) {
  ports.openPath?.(fullTarget(kind))?.catch((err: unknown) => {
    toast.error(
      t("richLink.openFailed", {
        error: err instanceof Error ? err.message : String(err),
      }),
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
      const tookOver =
        relPath !== null &&
        sessionId !== undefined &&
        (ports.previewFile?.(sessionId, relPath) ?? false);
      if (tookOver) return;
      openWithExternalApp(kind, t, ports);
      return;
    }
    case "local-external":
      openWithExternalApp(kind, t, ports);
      return;
    case "unknown":
      // 不拦截，让浏览器走默认行为（target=_blank fallback）。
      return;
  }
}

function URLPopover({ kind }: { kind: Extract<LinkClass, { kind: "url" }> }) {
  const { t } = useUiTranslation();

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <span className="inline-flex items-center gap-1 rounded-full bg-primary px-2 py-0.5 text-meta font-semibold text-primary-foreground">
          <LinkIcon className="size-3" aria-hidden /> {t("richLink.url")}
        </span>
        <div className="flex-1" />
        <button
          type="button"
          className="inline-flex items-center gap-1 rounded-md border border-border bg-secondary px-2 py-1 text-xs"
          onClick={() => copyToClipboard(kind.url, t)}
        >
          <CopyIcon className="size-3" aria-hidden /> {t("common.copy")}
        </button>
      </div>
      <code className="break-all font-mono text-xs text-foreground">
        {kind.url}
      </code>
      <div className="flex items-center gap-1.5 rounded-md bg-secondary px-2 py-1 text-meta text-muted-foreground">
        <MousePointerClick className="size-3" aria-hidden />
        {t("richLink.openInBrowser")}
      </div>
    </div>
  );
}

function LineChip({ line, col }: { line?: number; col?: number }) {
  if (line === undefined) return null;
  return (
    <span className="inline-flex items-center rounded-full border border-border bg-secondary px-2 py-0.5 font-mono text-meta">
      L{line}
      {col !== undefined ? `:${col}` : ""}
    </span>
  );
}

function LocalInternalPopover({
  kind,
  cwd,
}: {
  kind: Extract<LinkClass, { kind: "local-internal" }>;
  cwd: string;
}) {
  const { t } = useUiTranslation();
  const full = fullTarget(kind);
  const PathIcon = kind.pathKind === "folder" ? Folder : FileText;
  const label =
    kind.pathKind === "folder"
      ? t("richLink.localFolder")
      : t("richLink.localFile");
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <span className="inline-flex items-center gap-1 rounded-full bg-agent-2 px-2 py-0.5 text-meta font-semibold text-primary-foreground">
          <PathIcon className="size-3" aria-hidden /> {label}
        </span>
        <LineChip line={kind.line} col={kind.col} />
        <div className="flex-1" />
        <button
          type="button"
          className="inline-flex items-center gap-1 rounded-md border border-border bg-secondary px-2 py-1 text-xs"
          onClick={() => copyToClipboard(full, t)}
        >
          <CopyIcon className="size-3" aria-hidden /> {t("common.copy")}
        </button>
      </div>
      <div className="flex flex-col gap-0.5 rounded-md bg-secondary px-2.5 py-1.5">
        <div className="flex items-baseline gap-2">
          <span className="w-12 shrink-0 text-meta font-semibold text-muted-foreground">
            {t("richLink.projectRoot")}
          </span>
          <code className="min-w-0 flex-1 break-all whitespace-normal font-mono text-xs text-muted-foreground">
            {cwd}
          </code>
        </div>
        <div className="flex items-baseline gap-2">
          <span className="w-12 shrink-0 text-meta font-semibold text-muted-foreground">
            {t("richLink.relative")}
          </span>
          <code className="min-w-0 flex-1 break-all whitespace-normal font-mono text-xs font-semibold text-foreground">
            {kind.relPath}
          </code>
        </div>
      </div>
      <code className="break-all font-mono text-meta text-muted-foreground">
        {full}
      </code>
      <div className="flex items-center gap-1.5 rounded-md bg-secondary px-2 py-1 text-meta text-muted-foreground">
        <MousePointerClick className="size-3" aria-hidden />
        {t("richLink.clickToOpen")}
      </div>
    </div>
  );
}

function LocalExternalPopover({
  kind,
}: {
  kind: Extract<LinkClass, { kind: "local-external" }>;
}) {
  const { t } = useUiTranslation();
  const full = fullTarget(kind);
  const PathIcon = kind.pathKind === "folder" ? Folder : FileText;
  const label =
    kind.pathKind === "folder"
      ? t("richLink.localFolderExternal")
      : t("richLink.localFileExternal");
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <span className="inline-flex items-center gap-1 rounded-full bg-muted-foreground px-2 py-0.5 text-meta font-semibold text-background">
          <PathIcon className="size-3" aria-hidden /> {label}
        </span>
        <LineChip line={kind.line} col={kind.col} />
        <div className="flex-1" />
        <button
          type="button"
          className="inline-flex items-center gap-1 rounded-md border border-border bg-secondary px-2 py-1 text-xs"
          onClick={() => copyToClipboard(full, t)}
        >
          <CopyIcon className="size-3" aria-hidden /> {t("common.copy")}
        </button>
      </div>
      <code className="break-all font-mono text-xs font-semibold text-foreground">
        {full}
      </code>
      <div className="text-meta text-muted-foreground">
        {t("richLink.outsideCwd")}
      </div>
      <div className="flex items-center gap-1.5 rounded-md bg-secondary px-2 py-1 text-meta text-muted-foreground">
        <MousePointerClick className="size-3" aria-hidden />
        {t("richLink.openWithDefaultApp")}
      </div>
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
  const canPreview =
    typeof ports.previewFile === "function" &&
    sessionId !== undefined &&
    previewRelPath(kind) !== null;
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

  return (
    <HoverCard
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
      <HoverCardContent className="w-[min(28rem,calc(100vw-2rem))]">
        {kind.kind === "url" ? (
          <URLPopover kind={kind} />
        ) : kind.kind === "local-internal" ? (
          <LocalInternalPopover kind={kind} cwd={cwd ?? ""} />
        ) : (
          <LocalExternalPopover kind={kind} />
        )}
      </HoverCardContent>
    </HoverCard>
  );
}
