/**
 * 导入对话框的右栏：真实转录预览（规格「预览」，决策 14 / 11 / 16）。
 *
 * **不另造渲染器**：预览拿到的 `messages` 已经是后端投影好的块，与真实回放/重载
 * 同一条投影，所以直接喂进既有的 `buildSettledTranscriptRows` → `TranscriptRowView`
 * 渲染链。另写一个「预览专用」的简化渲染器等于第二套语义 —— 用户在预览里看到的
 * 工具卡会和导进去之后的不一样，而预览的全部意义正是「当场证明这条真解得出来」。
 *
 * 缺口在这里说一次（G1，决策 11）：导入**前**、决策点上。转录内那一行灰字（G3）
 * 由后端写成 `notice` 块，随 `messages` 一起来，不需要这里再做什么。
 */
import * as React from "react";
import { CircleAlert, Info, Loader2 } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { AgentBackendLogo } from "../engine/ai-brand-logo";
import { Button } from "../ui/button";
import { MESSAGE_AVATAR_CLASS } from "../transcript/message-row";
import { buildSettledTranscriptRows } from "../transcript/transcript-rows";
import {
  TranscriptRenderContext,
  TranscriptRowView,
} from "../transcript/transcript-row-view";
import type { TranscriptMessage } from "../transcript/dto";

import { useBackendLabel } from "./candidate-list";
import type { ImportPreviewResult } from "./ports";

export type PreviewState =
  | { kind: "idle" }
  | { kind: "loading" }
  /** 文件已被删、或者损坏到解不出任何一轮。导入按钮同时不可用（对话框负责）。 */
  | { kind: "error"; message: string }
  /** 这条已经在库里：不预览，直接给去处。 */
  | { kind: "imported"; sessionId: string }
  | { kind: "ready"; result: ImportPreviewResult };

export interface PreviewPaneProps {
  state: PreviewState;
  onOpenImported(sessionId: string): void;
}

function MetaRow({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="text-2xs text-muted-foreground">{label}</dt>
      <dd className="min-w-0 truncate font-mono text-2xs text-foreground">
        {value}
      </dd>
    </>
  );
}

function formatRange(startedAt: number, endedAt: number): string {
  const fmt = (at: number) => {
    if (!at) return "";
    const d = new Date(at);
    return `${d.getMonth() + 1}-${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  };
  const from = fmt(startedAt);
  const to = fmt(endedAt);
  if (from && to) return `${from} — ${to}`;
  return from || to;
}

/** 前几轮的真实转录。行模型与主对话流同一条，只是没有 live 内容。 */
function PreviewTranscript({
  messages,
  backend,
}: {
  messages: TranscriptMessage[];
  backend: string;
}) {
  const backendLabel = useBackendLabel();
  const rows = React.useMemo(
    () =>
      buildSettledTranscriptRows({
        autonomousIds: new Set<number>(),
        displayMessages: messages,
        localCommands: [],
      }).rows,
    [messages],
  );
  const ctx = React.useMemo(
    () => ({
      agentName: backendLabel(backend),
      // 预览时还没选 agent（agent 是 agentre 自己的概念，磁盘上没有），所以头像
      // 用后端徽标：这一栏问的是「这条转录长什么样」，不是「谁在说话」。
      agentAvatar: (
        <AgentBackendLogo
          backendType={backend}
          className={cn(MESSAGE_AVATAR_CLASS, "bg-secondary")}
        />
      ),
      // 预览不属于任何一条会话，也不接任何写动作：不传 onRerun / onEdit /
      // onStopSubagent，行视图据此渲染成只读。
      sessionId: 0,
    }),
    [backend, backendLabel],
  );

  return (
    <TranscriptRenderContext.Provider value={ctx}>
      <div data-testid="import-preview-transcript" className="flex flex-col">
        {rows.map((row) => (
          <TranscriptRowView
            key={row.key}
            row={row}
            liveTail=""
            liveBlocks={undefined}
            liveRetry={null}
            showIndicator={false}
            compacting={false}
            reconnecting={false}
          />
        ))}
      </div>
    </TranscriptRenderContext.Provider>
  );
}

export function PreviewPane({ state, onOpenImported }: PreviewPaneProps) {
  const { t } = useUiTranslation();
  const backendLabel = useBackendLabel();

  return (
    <div
      data-testid="import-preview"
      /*
        选中项一变，这一栏的内容整块换掉 —— 用 `aria-live` 把变化播报出去，
        否则键盘用户按方向键时右栏在无声地换，屏幕阅读器只念得到左栏那一行。
        `polite` 不打断当前朗读（扫描与导入的进度也在同一条规矩下）。
      */
      role="region"
      aria-live="polite"
      aria-label={t("importSession.previewAria")}
      className="flex min-h-0 flex-1 flex-col overflow-hidden"
    >
      {/*
        元信息（这是哪条会话、哪个目录、多少轮）钉在栏顶，只有转录滚：它就是
        「导不导这条」的全部依据，跟着转录滚走之后，屏幕上只剩一堆认不出归属
        的对话，而选中项一变右栏整块换掉，回头核对的成本还更高。
      */}
      {state.kind === "ready" ? (
        <div
          data-testid="import-preview-meta"
          className="flex shrink-0 flex-col gap-2 border-b border-border px-4 py-3"
        >
          <h3 className="truncate text-sm font-semibold text-foreground">
            {state.result.meta.title || t("importSession.untitled")}
          </h3>
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1">
            <MetaRow
              label={t("importSession.meta.backend")}
              value={[
                backendLabel(state.result.meta.backend),
                state.result.meta.model,
              ]
                .filter(Boolean)
                .join(" · ")}
            />
            <MetaRow
              label={t("importSession.meta.cwd")}
              value={state.result.meta.cwd}
            />
            <MetaRow
              label={t("importSession.meta.time")}
              value={formatRange(
                state.result.meta.startedAt,
                state.result.meta.endedAt,
              )}
            />
            <MetaRow
              label={t("importSession.meta.content")}
              value={[
                t("importSession.turns", { count: state.result.meta.turns }),
                t("importSession.meta.toolCalls", {
                  count: state.result.meta.toolCalls,
                }),
                t("importSession.meta.compactions", {
                  count: state.result.meta.compactions,
                }),
              ].join(" · ")}
            />
          </dl>
        </div>
      ) : null}

      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        {state.kind === "idle" ? (
          <p className="m-auto px-6 py-10 text-center text-xs text-muted-foreground">
            {t("importSession.preview.idle")}
          </p>
        ) : null}

        {state.kind === "loading" ? (
          <p
            data-testid="import-preview-loading"
            className="m-auto flex items-center gap-2 px-6 py-10 text-xs text-muted-foreground"
          >
            <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
            {t("importSession.preview.loading")}
          </p>
        ) : null}

        {state.kind === "error" ? (
          <div
            data-testid="import-preview-error"
            role="alert"
            className="m-4 flex items-start gap-2 rounded-md border border-status-error/40 bg-destructive-soft px-3 py-2.5 text-status-error"
          >
            <CircleAlert
              className="mt-0.5 size-3.5 shrink-0"
              aria-hidden="true"
            />
            <div className="flex min-w-0 flex-col gap-1">
              <span className="text-xs font-semibold">
                {t("importSession.preview.failedTitle")}
              </span>
              <span className="text-2xs break-words">{state.message}</span>
            </div>
          </div>
        ) : null}

        {state.kind === "imported" ? (
          <div
            data-testid="import-preview-imported"
            className="m-auto flex flex-col items-center gap-2 px-6 py-10 text-center"
          >
            <span className="text-xs text-muted-foreground">
              {t("importSession.preview.alreadyImported")}
            </span>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => onOpenImported(state.sessionId)}
            >
              {t("importSession.imported.open")}
            </Button>
          </div>
        ) : null}

        {state.kind === "ready" ? (
          <>
            {/* cwd 不存在：降级为只读导入（决策 16）。说清后果，不假装能跑。 */}
            {state.result.meta.cwdExists ? null : (
              <div
                data-testid="import-cwd-missing"
                role="alert"
                className="m-3 flex flex-col gap-1 rounded-md border border-status-error/40 bg-destructive-soft px-3 py-2.5"
              >
                <span className="text-xs font-semibold text-status-error">
                  {t("importSession.cwdMissing.title")}
                </span>
                <span className="text-2xs text-foreground">
                  {t("importSession.cwdMissing.body", {
                    path: state.result.meta.cwd,
                  })}
                </span>
              </div>
            )}

            {/* G1：缺口在导入**前**说一次，决策权还在用户手里。 */}
            {state.result.meta.gaps.length > 0 ? (
              <ul
                data-testid="import-preview-gaps"
                className="m-3 flex flex-col gap-1"
              >
                {state.result.meta.gaps.map((gap) => (
                  <li
                    key={`${gap.kind}:${gap.detail}`}
                    className="flex items-start gap-2 rounded-md bg-secondary/60 px-2.5 py-2 text-2xs text-muted-foreground"
                  >
                    <Info
                      className="mt-0.5 size-3 shrink-0"
                      aria-hidden="true"
                    />
                    <span className="min-w-0">{gap.text || gap.detail}</span>
                  </li>
                ))}
              </ul>
            ) : null}

            <div className="px-3 py-2">
              <PreviewTranscript
                messages={state.result.messages}
                backend={state.result.meta.backend}
              />
              {/* 预览末尾说清「后面还有多少轮」，别让人以为导的就是这几轮。 */}
              <p
                data-testid="import-preview-tail"
                className="px-2 py-3 text-2xs text-muted-foreground"
              >
                {state.result.remainingTurns > 0
                  ? t("importSession.preview.tail", {
                      count: state.result.meta.turns,
                    })
                  : state.result.remainingTurns === 0
                    ? t("importSession.preview.tailComplete")
                    : t("importSession.preview.tailUnknown")}
              </p>
            </div>
          </>
        ) : null}
      </div>
    </div>
  );
}
