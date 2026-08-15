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
import { classifyLink, type LinkClass } from "../lib/link-classify";
import { useTranscriptPorts } from "./ports-context";
import type { TranscriptPorts } from "./ports";

const HOVER_OPEN_DELAY_MS = 200;
const HOVER_CLOSE_DELAY_MS = 200;

type RichLinkProps = {
  href?: string;
  className?: string;
  cwd?: string;
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
    className: "inline-block size-3 shrink-0 align-text-bottom",
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
      className="inline-block size-3 shrink-0 align-text-bottom opacity-70"
      aria-hidden
    />
  );
}

async function copyToClipboard(text: string, t: TFunction) {
  try {
    await navigator.clipboard.writeText(text);
    toast.success(t("common.copied"), {
      duration: 2000,
      position: "bottom-right",
    });
  } catch {
    toast.error(t("common.copyFailed"));
  }
}

function dispatchClick(kind: LinkClass, t: TFunction, ports: TranscriptPorts) {
  switch (kind.kind) {
    case "url":
      ports.openExternalURL?.(kind.url);
      return;
    case "local-internal":
    case "local-external":
      ports.openPath?.(fullTarget(kind))?.catch((err: unknown) => {
        toast.error(
          t("richLink.openFailed", {
            error: err instanceof Error ? err.message : String(err),
          }),
        );
      });
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
        {t("richLink.openWithDefaultApp")}
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

export function RichLink({ href, className, cwd, children }: RichLinkProps) {
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

  const onClick = (e: React.MouseEvent) => {
    e.preventDefault();
    dispatchClick(kind, t, ports);
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
            "inline-flex items-baseline gap-1 text-primary-text underline underline-offset-2 hover:opacity-80",
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
