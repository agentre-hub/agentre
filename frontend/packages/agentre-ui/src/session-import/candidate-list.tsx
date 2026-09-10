/**
 * 导入对话框的左栏：候选列表（规格「UI 与状态」，决策 14）。
 *
 * 三件事在这里同时成立，缺一条这栏就不好用：
 *
 * 1. **按时间分组**（`buildCandidateGroups`）—— 同名会话在一条平列表里只差轮数与
 *    时间，认不出该导哪条；先按天切开，再靠右栏的预览分辨。
 * 2. **已导入的照常列出、不可选**（决策 18 的判重口径）—— 藏起来会让用户以为扫描
 *    漏了，所以留在原位、给「打开」的去处，并把不可选的**原因**读得出来
 *    （`aria-disabled` + 那一行说明），而不是只在视觉上变浅。
 * 3. **键盘上下移动并选中**，选中项的变化让右栏对辅助技术可感知 —— 列表是
 *    `role="listbox"` + `aria-activedescendant`，右栏是 `aria-live` 的区域。
 *
 * 已导入的行**照样能被方向键走到**：读不出原因的「跳过」等于把那条会话变没了。
 */
import * as React from "react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { AgentBackendLogo } from "../engine/ai-brand-logo";
import { Button } from "../ui/button";

import { buildCandidateGroups } from "./candidate-groups";
import type { CandidateBucket } from "./candidate-groups";
import type { ImportCandidateView } from "./ports";

/** 候选行的 DOM id：`aria-activedescendant` 指向它。 */
export function candidateOptionId(locator: string): string {
  return `import-candidate-${encodeURIComponent(locator)}`;
}

function useBucketLabel(): (bucket: CandidateBucket) => string {
  const { t } = useUiTranslation();
  return (bucket) => {
    if (bucket === "today") return t("importSession.groups.today");
    if (bucket === "yesterday") return t("importSession.groups.yesterday");
    return t("importSession.groups.earlier");
  };
}

/**
 * 后端短名。用既有的 `agentBackends.backendType.*` 那份文案，不另起一套 ——
 * 同一个后端在引擎设置里叫「Claude Code」、在这里叫别的，是同一件东西两个名字。
 *
 * 逐条写字面量 key 而不是拼模板：拼出来的 key 静态守卫查不了，漏一条要等界面上
 * 显示出原始 key 才发现。
 */
export function useBackendLabel(): (backend: string) => string {
  const { t } = useUiTranslation();
  return (backend) => {
    if (backend === "claudecode")
      return t("agentBackends.backendType.claudecode.shortLabel");
    if (backend === "codex")
      return t("agentBackends.backendType.codex.shortLabel");
    if (backend === "piagent")
      return t("agentBackends.backendType.piagent.shortLabel");
    return backend;
  };
}

/** 今天只报时刻，更早报月-日；年份不报（跨年那条靠分组头已经说清是「更早」）。 */
export function formatCandidateTime(at: number, now: number): string {
  if (!at) return "";
  const d = new Date(at);
  const today = new Date(now);
  today.setHours(0, 0, 0, 0);
  if (at >= today.getTime()) {
    return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  }
  return `${d.getMonth() + 1}-${d.getDate()}`;
}

export interface CandidateListProps {
  candidates: readonly ImportCandidateView[];
  /** 当前高亮那一条的 locator（不论可不可选）。 */
  activeLocator: string | null;
  onActivate(candidate: ImportCandidateView): void;
  onOpenImported(sessionId: string): void;
  /** 「现在」，由对话框一次算好传进来 —— 分组与时刻格式化都靠它，避免每行各取一次。 */
  now: number;
}

export function CandidateList({
  candidates,
  activeLocator,
  onActivate,
  onOpenImported,
  now,
}: CandidateListProps) {
  const { t } = useUiTranslation();
  const bucketLabel = useBucketLabel();
  const backendLabel = useBackendLabel();
  const groups = React.useMemo(
    () => buildCandidateGroups(candidates, now),
    [candidates, now],
  );
  /** 方向键走的是**摊平后的顺序**，与视觉顺序一致（分组头不参与）。 */
  const flat = React.useMemo(
    () => groups.flatMap((group) => group.items),
    [groups],
  );

  const move = React.useCallback(
    (delta: number) => {
      if (flat.length === 0) return;
      const current = flat.findIndex((c) => c.locator === activeLocator);
      const next = Math.min(
        flat.length - 1,
        Math.max(0, (current < 0 ? 0 : current) + delta),
      );
      onActivate(flat[next]);
    },
    [activeLocator, flat, onActivate],
  );

  const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      move(1);
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      move(-1);
      return;
    }
    if (event.key === "Home") {
      event.preventDefault();
      if (flat[0]) onActivate(flat[0]);
      return;
    }
    if (event.key === "End") {
      event.preventDefault();
      const last = flat[flat.length - 1];
      if (last) onActivate(last);
    }
  };

  return (
    <div
      data-testid="import-candidate-list"
      role="listbox"
      tabIndex={0}
      aria-label={t("importSession.listAria")}
      aria-activedescendant={
        activeLocator ? candidateOptionId(activeLocator) : undefined
      }
      onKeyDown={onKeyDown}
      className="flex flex-col gap-1 outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
    >
      {groups.map((group) => (
        <div key={group.bucket} className="flex flex-col gap-1">
          <div className="px-1 pt-1 text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
            {bucketLabel(group.bucket)}
          </div>
          {group.items.map((candidate) => {
            const active = candidate.locator === activeLocator;
            const reasonId = `${candidateOptionId(candidate.locator)}-reason`;
            return (
              <div
                key={candidate.locator}
                id={candidateOptionId(candidate.locator)}
                data-testid={`import-candidate-${candidate.providerSessionId}`}
                role="option"
                aria-selected={active}
                aria-disabled={candidate.imported || undefined}
                aria-describedby={candidate.imported ? reasonId : undefined}
                onClick={() => onActivate(candidate)}
                className={cn(
                  "flex cursor-pointer items-start gap-2 rounded-md border px-2.5 py-2 transition-colors",
                  active
                    ? "border-primary bg-sidebar-active-bg"
                    : "border-border hover:bg-accent/60",
                  // 不可选只退颜色、不撤内容：它得能被读出来。
                  candidate.imported && "opacity-70",
                )}
              >
                <AgentBackendLogo
                  backendType={candidate.backend}
                  className="mt-0.5 size-5 rounded-sm"
                />
                <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                  <span className="truncate text-xs font-medium text-foreground">
                    {candidate.title || t("importSession.untitled")}
                  </span>
                  <span className="flex min-w-0 flex-wrap items-center gap-1.5 text-2xs text-muted-foreground">
                    {candidate.imported ? (
                      <span
                        id={reasonId}
                        className="rounded-sm bg-muted px-1 py-0.5 text-3xs"
                      >
                        {t("importSession.imported.reason")}
                      </span>
                    ) : candidate.origin ? (
                      <span className="rounded-sm border border-border px-1 py-0.5 text-3xs">
                        {candidate.origin === "agentre"
                          ? t("importSession.origin.agentre")
                          : t("importSession.origin.terminal")}
                      </span>
                    ) : null}
                    <span>{backendLabel(candidate.backend)}</span>
                    <span aria-hidden="true">·</span>
                    <span>
                      {candidate.turns > 0
                        ? t("importSession.turns", { count: candidate.turns })
                        : t("importSession.turnsUnknown")}
                    </span>
                    <span aria-hidden="true">·</span>
                    <span>
                      {formatCandidateTime(
                        candidate.endedAt || candidate.startedAt,
                        now,
                      )}
                    </span>
                  </span>
                </div>
                {candidate.imported ? (
                  <Button
                    type="button"
                    variant="outline"
                    size="xs"
                    data-testid={`import-open-${candidate.providerSessionId}`}
                    onClick={(e) => {
                      e.stopPropagation();
                      onOpenImported(candidate.importedSessionId);
                    }}
                  >
                    {t("importSession.imported.open")}
                  </Button>
                ) : null}
              </div>
            );
          })}
        </div>
      ))}
    </div>
  );
}
