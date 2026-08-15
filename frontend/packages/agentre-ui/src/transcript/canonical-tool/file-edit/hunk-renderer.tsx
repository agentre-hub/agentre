import { TranscriptPill } from "../../transcript-card";
import { cn } from "../../../lib/utils";
import { useUiTranslation } from "../../../i18n";
import type { DiffHunk, DiffLine, FileEditPatch } from "../types";

// FileBlock 渲染单个文件的 diff(可能多 hunks);showHeader=true 时画出文件名条。
export function FileBlock({
  file,
  showHeader,
}: {
  file: FileEditPatch;
  showHeader: boolean;
}) {
  const { t } = useUiTranslation();
  const empty = !file.hunks || file.hunks.length === 0;
  return (
    <div>
      {showHeader && (
        <div className="flex items-center gap-2 border-y border-border bg-secondary px-3 py-1.5">
          <span className="font-mono text-meta font-semibold text-foreground">
            {file.path}
          </span>
          {file.kind === "created" && (
            <TranscriptPill tone="done">
              {t("canonical.fileEdit.badge.new")}
            </TranscriptPill>
          )}
          {file.kind === "deleted" && (
            <TranscriptPill tone="error">
              {t("canonical.fileEdit.badge.deleted")}
            </TranscriptPill>
          )}
          <span className="ml-auto font-mono text-meta font-semibold text-status-running">
            +{file.plus}
          </span>
          {file.minus > 0 && (
            <span className="font-mono text-meta font-semibold text-destructive">
              −{file.minus}
            </span>
          )}
        </div>
      )}
      {empty ? (
        <div className="px-3 py-2 text-muted-foreground">
          {t("canonical.fileEdit.noChanges")}
        </div>
      ) : (
        // diff 行不换行(whitespace-pre),超宽的行必须能横向滚动 —— 卡片本身
        // 是 overflow-hidden,少了这层滚动容器长行就被直接裁掉且拖不出来。
        <div data-testid="file-edit-diff-scroll" className="overflow-x-auto">
          {file.hunks.map((hunk, hi) => (
            <HunkBlock key={hi} hunk={hunk} />
          ))}
        </div>
      )}
      {file.truncated && (
        <div className="border-t border-border bg-secondary px-3 py-1 text-meta text-muted-foreground">
          {t("canonical.fileEdit.truncated", {
            count: file.plus + file.minus,
            shown: 200,
          })}
        </div>
      )}
    </div>
  );
}

function HunkBlock({ hunk }: { hunk: DiffHunk }) {
  return (
    <>
      <div className="w-max min-w-full bg-secondary px-3 py-1 font-mono text-meta font-semibold text-muted-foreground">
        @@ -{hunk.oldStart},{hunk.oldLines} +{hunk.newStart},{hunk.newLines} @@
        {hunk.header ? (
          <span className="ml-3 font-normal text-subtle-foreground">
            {hunk.header}
          </span>
        ) : null}
      </div>
      {hunk.lines.map((l, li) => (
        <DiffLineRow key={li} line={l} />
      ))}
    </>
  );
}

function DiffLineRow({ line }: { line: DiffLine }) {
  const bg =
    line.op === "+"
      ? "bg-status-running-bg"
      : line.op === "-"
        ? "bg-destructive-soft"
        : "";
  const markColor =
    line.op === "+"
      ? "text-status-running"
      : line.op === "-"
        ? "text-destructive"
        : "text-subtle-foreground";
  return (
    // w-max + min-w-full:行盒撑到最长行的宽度,横向滚动后 +/- 的背景条
    // 才会一路铺到行尾,而不是断在卡片可视边缘。
    <div className={cn("flex w-max min-w-full items-center px-3 py-0.5", bg)}>
      <span className="w-8 shrink-0 text-right text-meta text-subtle-foreground">
        {line.old ?? " "}
      </span>
      <span className="w-8 shrink-0 text-right text-meta text-subtle-foreground">
        {line.new ?? " "}
      </span>
      <span
        className={cn(
          "w-5 shrink-0 text-center text-meta font-semibold",
          markColor,
        )}
      >
        {line.op}
      </span>
      <span className="whitespace-pre text-foreground">{line.text}</span>
    </div>
  );
}
