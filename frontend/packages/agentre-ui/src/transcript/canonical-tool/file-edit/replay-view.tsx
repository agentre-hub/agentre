import type { ReactNode } from "react";

import { useUiTranslation } from "../../../i18n";
import type { DiffHunk, FileEditPatch } from "../types";

import { FileBlock } from "./hunk-renderer";
import type {
  ReplayFailureReason,
  ReplayResult,
  ReplaySegment,
} from "./replay";

// 静态 key 表：重放失败的每一种原因各有一句「为什么没能合并成一个连续 diff」。
// 不用模板字符串拼 key —— 静态 t("...") 才被 i18n 守卫测试收得到。
const REASON_KEY: Record<ReplayFailureReason, string> = {
  writeOverEdits: "canonical.fileEdit.replay.reason.writeOverEdits",
  anchorNotFound: "canonical.fileEdit.replay.reason.anchorNotFound",
  ambiguousAnchor: "canonical.fileEdit.replay.reason.ambiguousAnchor",
  truncatedMidway: "canonical.fileEdit.replay.reason.truncatedMidway",
};

/**
 * ReplayedFileDiff 画的是「本次会话里工具把这个文件改成了什么样」——
 * `replayPatches` 重放出来的那一个连续 diff（spec 决策 4）。
 *
 * 它的另一半职责是把**失败与降级说出来**：全量写入没有比较对象、某次调用被
 * 产出方截断、重放合不出一致结果，三种情况都必须在界面上看得见。重放合不出来
 * 时按调用顺序分段列出各次改动，并说明为什么没能合并——「一个空 diff」不是可
 * 接受的降级。
 */
export function ReplayedFileDiff({
  path,
  result,
}: {
  path: string;
  result: ReplayResult;
}) {
  const { t } = useUiTranslation();

  if (!result.ok) {
    return (
      <div data-testid="replayed-file-diff">
        <Notice tone="warning">
          {t("canonical.fileEdit.replay.fallbackIntro")}
        </Notice>
        <Notice>{t(REASON_KEY[result.reason])}</Notice>
        {result.segments.map((segment) => (
          <SegmentBlock key={segment.index} path={path} segment={segment} />
        ))}
      </div>
    );
  }

  return (
    <div data-testid="replayed-file-diff">
      {result.wholeFileWrite && (
        <Notice>{t("canonical.fileEdit.replay.wholeWrite")}</Notice>
      )}
      {result.truncatedCalls.length > 0 && (
        <Notice tone="warning">
          {t("canonical.fileEdit.replay.truncatedCalls", {
            n: result.truncatedCalls.length,
          })}
        </Notice>
      )}
      {result.hunks.length === 0 ? (
        <Notice>{t("canonical.fileEdit.replay.emptyResult")}</Notice>
      ) : (
        <FileBlock
          file={patchOf(path, result.hunks, result.plus, result.minus)}
          showHeader={false}
        />
      )}
    </div>
  );
}

function SegmentBlock({
  path,
  segment,
}: {
  path: string;
  segment: ReplaySegment;
}) {
  const { t } = useUiTranslation();
  return (
    <div data-testid="replayed-file-diff-segment">
      <div className="flex items-center gap-2 border-y border-border bg-secondary px-3 py-1.5">
        <span className="text-meta font-semibold text-foreground">
          {t("canonical.fileEdit.replay.segmentLabel", {
            index: segment.index + 1,
          })}
        </span>
        <span className="ml-auto font-mono text-meta font-semibold text-status-running">
          +{segment.plus}
        </span>
        {segment.minus > 0 && (
          <span className="font-mono text-meta font-semibold text-destructive">
            −{segment.minus}
          </span>
        )}
      </div>
      {segment.wholeFileWrite && (
        <Notice>{t("canonical.fileEdit.replay.segmentWholeWrite")}</Notice>
      )}
      {segment.truncated && (
        <Notice tone="warning">
          {t("canonical.fileEdit.replay.segmentTruncated")}
        </Notice>
      )}
      <FileBlock
        file={patchOf(path, segment.hunks, segment.plus, segment.minus)}
        showHeader={false}
      />
    </div>
  );
}

/**
 * 重放的产物不是任何一次真实调用，但正文渲染器吃的是 `FileEditPatch`：这里只是
 * 把 hunks 套进那个形状复用它，`kind` 恒为 modified（首列的四种状态由「变更」行
 * 负责，diff 正文不再重复表态），`truncated` 恒为 false（截断由上面的标注说，
 * 而不是套用单次调用那句「显示前 200 行」）。
 */
function patchOf(
  path: string,
  hunks: DiffHunk[],
  plus: number,
  minus: number,
): FileEditPatch {
  return { path, kind: "modified", hunks, plus, minus };
}

function Notice({ children, tone }: { children: ReactNode; tone?: "warning" }) {
  return (
    <div
      className={
        tone === "warning"
          ? "border-b border-border bg-destructive-soft px-3 py-1.5 text-meta text-foreground"
          : "border-b border-border bg-secondary px-3 py-1.5 text-meta text-muted-foreground"
      }
    >
      {children}
    </div>
  );
}
