import * as React from "react";
import { ChevronDown, Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";

import { classifyAppError, type AppError } from "@/lib/app-error";

import { ProjectMerge, ProjectMergePreview } from "../../../wailsjs/go/app/App";
import type { app } from "../../../wailsjs/go/models";
import {
  Button,
  cn,
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Popover,
  PopoverContent,
  PopoverTrigger,
  SearchInput,
} from "@agentre-hub/agentre-ui";

export type ProjectMergeCandidate = {
  id: number;
  name: string;
  /** 根 → 直接父级的名称链；顶层项目为空或不给。 */
  parentPath?: string[];
};
export type ProjectMergeSource = { id: number; name: string };

type PreviewState =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "ready"; preview: app.ProjectMergePreview }
  | { kind: "failed" };

/**
 * 合并候选：除 source 之外的每个项目，带上根 → 直接父级的名称链（顺着 parentID
 * 往上走）。父链断在找不到的项目就停；遇到环也停，不会走不完。
 */
export function mergeCandidates(
  projects: Iterable<app.ProjectItem>,
  sourceID: number,
): ProjectMergeCandidate[] {
  const byID = new Map<number, app.ProjectItem>();
  for (const p of projects) byID.set(p.id, p);
  const out: ProjectMergeCandidate[] = [];
  for (const p of byID.values()) {
    if (p.id === sourceID) continue;
    const parentPath: string[] = [];
    const seen = new Set<number>([p.id]);
    for (
      let cur = byID.get(p.parentID);
      cur && !seen.has(cur.id);
      cur = byID.get(cur.parentID)
    ) {
      seen.add(cur.id);
      parentPath.unshift(cur.name);
    }
    out.push({ id: p.id, name: p.name, parentPath });
  }
  return out;
}

function candidateLabel(c: ProjectMergeCandidate): string {
  return [...(c.parentPath ?? []), c.name].join(" / ");
}

/**
 * ProjectMergeDialog —— R11a「合并到已有项目」：把 source 与用户选定的另一个本地
 * 项目合并。哪一边最终留下由后端 chooseMergeWinner 决定，与用户选的方向无关；选好
 * 目标后向后端要一份只读预览（ProjectMergePreview）照着显示，前端不猜。
 */
export function ProjectMergeDialog({
  source,
  candidates,
  onClose,
  onMerged,
}: {
  source: ProjectMergeSource | null;
  candidates: ProjectMergeCandidate[];
  onClose: () => void;
  /** 合并成了。`keptProjectID` 是留下来的那一行 —— 哪边赢由后端定，这里只转述。 */
  onMerged: (keptProjectID: number) => void;
}) {
  const { t } = useTranslation();
  const [targetID, setTargetID] = React.useState<number | null>(null);
  // 每选一次自增：同一个目标再选一次也会重新要预览（预览失败后的出口）。
  const [pickNonce, setPickNonce] = React.useState(0);
  const [preview, setPreview] = React.useState<PreviewState>({ kind: "idle" });
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState<AppError | null>(null);

  React.useEffect(() => {
    setTargetID(null);
    setPreview({ kind: "idle" });
    setBusy(false);
    setErr(null);
  }, [source?.id]);

  const sourceID = source?.id ?? null;
  React.useEffect(() => {
    if (sourceID === null || targetID === null) return;
    let stale = false;
    setPreview({ kind: "loading" });
    ProjectMergePreview(sourceID, targetID).then(
      (p) => {
        if (!stale) setPreview({ kind: "ready", preview: p });
      },
      () => {
        if (!stale) setPreview({ kind: "failed" });
      },
    );
    return () => {
      stale = true;
    };
  }, [sourceID, targetID, pickNonce]);

  const handleMerge = async () => {
    if (source === null || targetID === null) return;
    setErr(null);
    setBusy(true);
    try {
      const kept = await ProjectMerge({ sourceID: source.id, targetID });
      onMerged(kept?.id ?? targetID);
      // 成了就关：留在窗里只剩一颗重新亮起来的「合并」，再点一次是「项目不存在」。
      onClose();
    } catch (e) {
      setErr(classifyAppError(e));
    } finally {
      setBusy(false);
    }
  };

  const ready = preview.kind === "ready" ? preview.preview : null;

  return (
    <Dialog
      open={source !== null}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {t("projectMerge.title", { name: source?.name ?? "" })}
          </DialogTitle>
        </DialogHeader>
        <DialogBody className="flex flex-col gap-3">
          <p className="text-2xs text-muted-foreground">
            {t("projectMerge.description")}
          </p>
          <div className="flex flex-col gap-1.5 text-xs">
            <span className="font-medium text-foreground">
              {t("projectMerge.pickTargetLabel")}
            </span>
            {candidates.length === 0 ? (
              <p className="text-2xs text-muted-foreground">
                {t("projectMerge.noCandidates")}
              </p>
            ) : (
              <MergeTargetPicker
                candidates={candidates}
                value={targetID}
                onPick={(id) => {
                  setTargetID(id);
                  setPickNonce((n) => n + 1);
                  setErr(null);
                }}
              />
            )}
          </div>
          <div className="flex flex-col gap-1.5 rounded-md bg-muted p-3">
            {ready ? (
              <>
                <PreviewLine
                  testId="project-merge-keep"
                  label={t("projectMerge.preview.keep")}
                  side={ready.keep}
                />
                <PreviewLine
                  testId="project-merge-drop"
                  label={t("projectMerge.preview.drop")}
                  side={ready.drop}
                />
              </>
            ) : null}
            <NoteLine>{t("projectMerge.reassignNote")}</NoteLine>
            <NoteLine>{t("projectMerge.keepPathNote")}</NoteLine>
          </div>
          {preview.kind === "failed" ? (
            <div className="rounded-md bg-destructive-soft px-3 py-2 text-2xs text-destructive-text">
              {t("projectMerge.errors.previewFailed")}
            </div>
          ) : null}
          {err ? <MergeFailure err={err} /> : null}
        </DialogBody>
        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={onClose}
          >
            {t("projectMerge.cancel")}
          </Button>
          <Button
            type="button"
            size="sm"
            disabled={!ready || busy}
            onClick={() => void handleMerge()}
          >
            {busy || preview.kind === "loading" ? (
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
            ) : null}
            {ready
              ? t("projectMerge.confirmAs", { name: ready.keep.name })
              : t("projectMerge.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * 合并失败（S5）：先说事实，再按类别给出口。项目已经不在了 → 回去重新选一个；
 * 其余说「合并失败」，后端带码的那句人话作正文，机器原话只进默认折叠的「详情」。
 */
function MergeFailure({ err }: { err: AppError }) {
  const { t } = useTranslation();
  const notFound = err.kind === "notFound";
  return (
    <div
      role="alert"
      className="flex flex-col gap-1 rounded-md bg-destructive-soft px-3 py-2 text-2xs text-destructive-text"
    >
      <p className="font-medium">
        {notFound
          ? t("projectMerge.errors.notFound")
          : t("projectMerge.errors.mergeFailed")}
      </p>
      {!notFound && err.message ? <p>{err.message}</p> : null}
      {err.detail ? (
        <details>
          <summary className="cursor-pointer">
            {t("projects.errors.details")}
          </summary>
          <pre
            data-selectable-text="true"
            className="mt-1 font-mono text-2xs break-all whitespace-pre-wrap"
          >
            {err.detail}
          </pre>
        </details>
      ) : null}
    </div>
  );
}

/** 说明列表的一行：项目符号用中性的装饰色，不借状态色。 */
function NoteLine({
  children,
  testId,
}: {
  children: React.ReactNode;
  testId?: string;
}) {
  return (
    <div className="flex items-baseline gap-2" data-testid={testId}>
      <span
        aria-hidden="true"
        className="mt-1.5 size-1.5 shrink-0 rounded-full bg-decorative-foreground"
      />
      <p className="text-2xs leading-relaxed">{children}</p>
    </div>
  );
}

function PreviewLine({
  testId,
  label,
  side,
}: {
  testId: string;
  label: string;
  side: app.ProjectMergePreviewSide;
}) {
  const { t } = useTranslation();
  const path = side.parentPath ?? [];
  const location =
    path.length > 0
      ? t("projectMerge.preview.under", { path: path.join(" / ") })
      : t("projectMerge.preview.topLevel");
  const origin = side.claimed
    ? t("projectMerge.preview.accountSide")
    : t("projectMerge.preview.localOnly");
  return (
    <NoteLine testId={testId}>
      {label} <span className="font-medium text-foreground">{side.name}</span>{" "}
      <span className="text-muted-foreground">
        {t("projectMerge.preview.meta", { location, origin })}
      </span>
    </NoteLine>
  );
}

/**
 * 合并目标选择器：候选按「父项目 / 子项目」的路径显示（不同父项目下的同名项目
 * 靠它分开），弹层顶部的输入框按路径过滤。
 */
function MergeTargetPicker({
  candidates,
  value,
  onPick,
}: {
  candidates: ProjectMergeCandidate[];
  value: number | null;
  onPick: (id: number) => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const listID = React.useId();

  const sorted = React.useMemo(
    () =>
      [...candidates].sort((a, b) =>
        candidateLabel(a).localeCompare(candidateLabel(b)),
      ),
    [candidates],
  );
  const selected = candidates.find((c) => c.id === value) ?? null;
  const needle = query.trim().toLowerCase();
  const visible = needle
    ? sorted.filter((c) => candidateLabel(c).toLowerCase().includes(needle))
    : sorted;

  const choose = (id: number) => {
    onPick(id);
    setOpen(false);
    setQuery("");
  };

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setQuery("");
      }}
    >
      <PopoverTrigger asChild>
        <button
          type="button"
          role="combobox"
          aria-expanded={open}
          aria-controls={listID}
          aria-label={t("projectMerge.pickTargetLabel")}
          className="flex h-9 w-full items-center justify-between gap-2 rounded-md bg-field-fill px-3 text-left text-xs hover:bg-field-fill-hover focus-visible:focus-outline"
        >
          {selected ? (
            <CandidateText candidate={selected} />
          ) : (
            <span className="truncate text-muted-foreground">
              {t("projectMerge.pickTargetPlaceholder")}
            </span>
          )}
          <ChevronDown
            className="size-3.5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="w-(--radix-popover-trigger-width) p-1"
      >
        <SearchInput
          value={query}
          onChange={setQuery}
          variant="bare"
          size="xs"
          className="px-2 py-1.5"
          placeholder={t("projectMerge.filterPlaceholder")}
          aria-label={t("projectMerge.filterPlaceholder")}
          autoFocus
        />
        <div
          id={listID}
          role="listbox"
          aria-label={t("projectMerge.pickTargetLabel")}
          className="max-h-60 overflow-y-auto"
        >
          {visible.map((c) => (
            <div
              key={c.id}
              role="option"
              aria-selected={c.id === value}
              tabIndex={0}
              className={cn(
                "flex cursor-pointer items-center rounded-md px-2 py-1.5 text-xs text-foreground outline-none hover:bg-accent focus-visible:bg-accent",
                c.id === value && "bg-primary-soft text-primary-text",
              )}
              onClick={() => choose(c.id)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  choose(c.id);
                }
              }}
            >
              <CandidateText candidate={c} />
            </div>
          ))}
        </div>
        {visible.length === 0 ? (
          <p className="px-2 py-1.5 text-2xs text-muted-foreground">
            {t("projectMerge.noMatches")}
          </p>
        ) : null}
      </PopoverContent>
    </Popover>
  );
}

function CandidateText({ candidate }: { candidate: ProjectMergeCandidate }) {
  const path = candidate.parentPath ?? [];
  return (
    <span className="truncate">
      {path.length > 0 ? (
        <span className="text-muted-foreground">{`${path.join(" / ")} / `}</span>
      ) : null}
      {candidate.name}
    </span>
  );
}
