import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  Check,
  CheckCircle2,
  ChevronDown,
  Circle,
  CircleDot,
  CornerDownLeft,
  MessageSquareQuote,
  PencilLine,
  Square,
  SquareCheck,
} from "lucide-react";
import {
  clearTranscriptDraftState,
  loadTranscriptDraftState,
  saveTranscriptDraftState,
  useCollapsible,
  TranscriptCard,
  TranscriptCardBody,
  TranscriptPill,
  useTranscriptBooleanState,
} from "@agentre-ai/agentre-ui";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

import type { CanonicalCardProps } from "../props";
import type {
  AskQuestionDTO,
  AskAnswerDTO,
  CanonicalDTO,
  UserAskDTO,
} from "../types";
import { useSubmitAnswer } from "./use-submit-answer";

// OTHER_LABEL 与后端 canonical.OtherAnswerLabel 对齐。
const OTHER_LABEL = "__other__";

type Selection = { labels: string[]; otherText: string };
type UserAskDraftState = { activeQIdx: number; selections: Selection[] };

function readUserAsk(toolBlock: unknown): UserAskDTO | undefined {
  const c = (toolBlock as { canonical?: CanonicalDTO }).canonical;
  if (!c || c.kind !== "user.ask") return undefined;
  return c.userAsk;
}

function initialSelections(payload: UserAskDTO | undefined): Selection[] {
  const qs = payload?.questions ?? [];
  if (!payload?.answered || !payload?.answers?.length) {
    return qs.map(() => ({ labels: [], otherText: "" }));
  }
  return qs.map((_, i) => {
    const ans = payload.answers!.find((a) => a.questionIndex === i);
    if (!ans) return { labels: [], otherText: "" };
    return { labels: [...(ans.labels ?? [])], otherText: ans.otherText ?? "" };
  });
}

// UserAskCard 渲染 canonical.user.ask —— 结构化问答(claudecode AskUserQuestion /
// codex request_user_input)。读 toolBlock.canonical.userAsk。
export const UserAskCard: React.FC<CanonicalCardProps> = ({
  toolBlock,
  sessionId,
  uiStateKey,
  tabStateKey,
}) => {
  const { t } = useTranslation();
  const submitAnswer = useSubmitAnswer();
  const payload = readUserAsk(toolBlock);
  const draftKey =
    payload?.requestId && !payload.answered && !payload.skipped
      ? `userAsk:${payload.requestId}`
      : undefined;

  const [collapsed, setCollapsed] = useTranscriptBooleanState(
    uiStateKey,
    false,
  );
  const { mounted, onTransitionEnd } = useCollapsible(!collapsed);
  const [selections, setSelections] = React.useState<Selection[]>(() => {
    const saved = loadTranscriptDraftState<UserAskDraftState>(
      tabStateKey,
      draftKey,
    );
    return saved?.selections ?? initialSelections(payload);
  });
  const [submitting, setSubmitting] = React.useState(false);
  const [failed, setFailed] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const totalQs = payload?.questions?.length ?? 0;
  const [activeQIdx, setActiveQIdx] = React.useState(() => {
    const saved = loadTranscriptDraftState<UserAskDraftState>(
      tabStateKey,
      draftKey,
    );
    const maxIdx = Math.max(0, (payload?.questions?.length ?? 1) - 1);
    return Math.min(Math.max(saved?.activeQIdx ?? 0, 0), maxIdx);
  });
  const draftClearedRef = React.useRef(false);

  const isAnswered = !!payload?.answered;
  const isSkipped = !!payload?.skipped;
  const isExpired = !!payload?.expired || failed;
  const isLocked = isAnswered || isSkipped || isExpired || submitting;

  React.useEffect(() => {
    if (isAnswered || isSkipped) {
      draftClearedRef.current = true;
      clearTranscriptDraftState(tabStateKey, draftKey);
      return;
    }
    if (draftClearedRef.current) return;
    saveTranscriptDraftState<UserAskDraftState>(tabStateKey, draftKey, {
      activeQIdx,
      selections,
    });
  }, [activeQIdx, draftKey, isAnswered, isSkipped, selections, tabStateKey]);

  const toggleOption = React.useCallback(
    (qIdx: number, label: string, multi: boolean) => {
      setSelections((prev) => {
        const next = prev.slice();
        const cur = next[qIdx] ?? { labels: [], otherText: "" };
        if (multi) {
          const has = cur.labels.includes(label);
          next[qIdx] = {
            ...cur,
            labels: has
              ? cur.labels.filter((l) => l !== label)
              : [...cur.labels, label],
          };
        } else {
          next[qIdx] = {
            ...cur,
            labels: cur.labels[0] === label ? [] : [label],
          };
        }
        return next;
      });
    },
    [],
  );

  const setOtherText = React.useCallback((qIdx: number, text: string) => {
    setSelections((prev) => {
      const next = prev.slice();
      const cur = next[qIdx] ?? { labels: [], otherText: "" };
      next[qIdx] = { ...cur, otherText: text };
      return next;
    });
  }, []);

  const handleSubmit = React.useCallback(
    async (overrideSkipped?: boolean) => {
      if (!payload?.requestId || !sessionId) return;
      if (isLocked) return;
      setError(null);
      const skipped = !!overrideSkipped;
      if (!skipped) {
        for (let i = 0; i < (payload.questions ?? []).length; i++) {
          const sel = selections[i];
          if (!sel || sel.labels.length === 0) {
            setError(t("canonical.userAsk.errors.optionRequired"));
            return;
          }
          if (sel.labels.includes(OTHER_LABEL) && !sel.otherText.trim()) {
            setError(t("canonical.userAsk.errors.otherRequired"));
            return;
          }
        }
      }
      setSubmitting(true);
      try {
        const answers: AskAnswerDTO[] = skipped
          ? []
          : selections.map((sel, idx) => ({
              questionIndex: idx,
              labels: [...sel.labels],
              otherText: sel.otherText,
            }));
        await submitAnswer({
          sessionId,
          requestId: payload.requestId,
          answers: skipped ? undefined : answers,
          skipped,
        });
        draftClearedRef.current = true;
        clearTranscriptDraftState(tabStateKey, draftKey);
        setCollapsed(true);
      } catch {
        setFailed(true);
        setError(t("canonical.userAsk.errors.expired"));
      } finally {
        setSubmitting(false);
      }
    },
    [
      payload,
      sessionId,
      selections,
      isLocked,
      submitAnswer,
      t,
      setCollapsed,
      tabStateKey,
      draftKey,
    ],
  );

  const headerLabel = React.useMemo(() => {
    if (!payload?.questions?.length) return "";
    return payload.questions[activeQIdx]?.header ?? "";
  }, [payload, activeQIdx]);

  if (!payload?.questions?.length) return null;

  return (
    <TranscriptCard
      data-testid="user-ask-card"
      data-selectable-text="true"
      className={cn("text-foreground outline-none", isLocked && "opacity-95")}
    >
      <button
        type="button"
        onClick={() => setCollapsed((v) => !v)}
        aria-expanded={!collapsed}
        className="flex w-full items-center gap-2 border-b border-border px-3.5 py-2.5 text-left transition-colors hover:bg-muted/40"
      >
        <ChevronDown
          aria-hidden="true"
          className={cn(
            "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform duration-150 ease-out",
            collapsed && "-rotate-90",
          )}
        />
        <MessageSquareQuote className="h-3.5 w-3.5 shrink-0 text-primary" />
        <span className="font-mono text-xs font-semibold text-primary">
          user_ask
        </span>
        {headerLabel && (
          <>
            <span className="font-mono text-xs text-muted-foreground">·</span>
            <span className="rounded-sm border border-primary/30 bg-primary/10 px-1.5 py-0.5 text-meta font-medium text-primary">
              {headerLabel}
            </span>
          </>
        )}
        <div className="flex-1" />
        <StatusPill
          answered={isAnswered}
          skipped={isSkipped}
          expired={isExpired}
        />
      </button>

      <div
        aria-hidden={collapsed}
        onTransitionEnd={onTransitionEnd}
        className="grid transition-[grid-template-rows] duration-200 ease-out motion-reduce:transition-none"
        style={{ gridTemplateRows: collapsed ? "0fr" : "1fr" }}
      >
        <div className="min-h-0 overflow-hidden">
          {mounted ? (
            <div className="min-h-0 overflow-hidden">
              {totalQs > 1 && (
                <QuestionTabs
                  questions={payload.questions}
                  activeIdx={activeQIdx}
                  selections={selections}
                  onSelect={setActiveQIdx}
                />
              )}
              {/* header 已用 border-b 分隔(单问题时是唯一分隔线,多问题时 QuestionTabs
          再补一条 border-b),这里不重复套 TranscriptCardBody 的 border-t,只借它的
          水平内边距 token,避免在 tabs/内容边界叠出两条相邻的分隔线。 */}
              <TranscriptCardBody className="flex flex-col gap-3 border-t-0">
                {payload.questions[activeQIdx] && (
                  <QuestionGroup
                    q={payload.questions[activeQIdx]}
                    qIdx={activeQIdx}
                    sel={selections[activeQIdx]}
                    locked={isLocked}
                    onToggle={toggleOption}
                    onOther={setOtherText}
                  />
                )}
                {error && (
                  <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                    {error}
                  </div>
                )}
              </TranscriptCardBody>

              {!isLocked && (
                <div className="flex items-center gap-2 border-t border-border px-3.5 py-2.5">
                  <div className="flex-1" />
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={submitting}
                    onClick={() => void handleSubmit(true)}
                  >
                    {t("common.skip")}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    disabled={submitting}
                    onClick={() => void handleSubmit(false)}
                    className="gap-1.5"
                  >
                    {t("canonical.userAsk.submit")}
                    <CornerDownLeft className="h-3 w-3" />
                  </Button>
                </div>
              )}
            </div>
          ) : null}
        </div>
      </div>
    </TranscriptCard>
  );
};

function StatusPill({
  answered,
  skipped,
  expired,
}: {
  answered: boolean;
  skipped: boolean;
  expired: boolean;
}) {
  const { t } = useTranslation();
  if (expired) {
    return (
      <TranscriptPill tone="default">
        <span className="h-1.5 w-1.5 rounded-full bg-muted-foreground" />
        {t("canonical.userAsk.expired")}
      </TranscriptPill>
    );
  }
  if (skipped) {
    return (
      <TranscriptPill tone="default">
        <span className="h-1.5 w-1.5 rounded-full bg-muted-foreground" />
        {t("canonical.userAsk.skipped")}
      </TranscriptPill>
    );
  }
  if (answered) {
    return (
      <TranscriptPill tone="done">
        <Check className="h-2.5 w-2.5" />
        {t("canonical.userAsk.answered")}
      </TranscriptPill>
    );
  }
  return (
    <TranscriptPill tone="pending">
      <span className="h-1.5 w-1.5 rounded-full bg-status-waiting" />
      {t("canonical.userAsk.waiting")}
    </TranscriptPill>
  );
}

function QuestionTabs({
  questions,
  activeIdx,
  selections,
  onSelect,
}: {
  questions: AskQuestionDTO[];
  activeIdx: number;
  selections: Selection[];
  onSelect: (idx: number) => void;
}) {
  return (
    <div className="flex items-end gap-1 border-b border-border px-3.5">
      {questions.map((q, idx) => {
        const active = idx === activeIdx;
        const answered = (selections[idx]?.labels.length ?? 0) > 0;
        const label = q.header ? `Q${idx + 1} · ${q.header}` : `Q${idx + 1}`;
        return (
          <button
            key={idx}
            type="button"
            onClick={() => onSelect(idx)}
            className={cn(
              "-mb-px flex items-center gap-1.5 border-b-2 px-3.5 pt-2.5 pb-2 text-xs transition-colors",
              active
                ? "border-primary font-semibold text-primary"
                : "border-transparent font-medium text-muted-foreground hover:text-foreground",
            )}
          >
            {answered ? (
              <CheckCircle2
                className={cn(
                  "h-3 w-3",
                  active ? "text-primary" : "text-status-running/70",
                )}
              />
            ) : (
              <Circle className="h-3 w-3 text-muted-foreground/60" />
            )}
            <span>{label}</span>
          </button>
        );
      })}
    </div>
  );
}

function QuestionGroup({
  q,
  qIdx,
  sel,
  locked,
  onToggle,
  onOther,
}: {
  q: AskQuestionDTO;
  qIdx: number;
  sel: Selection | undefined;
  locked: boolean;
  onToggle: (qIdx: number, label: string, multi: boolean) => void;
  onOther: (qIdx: number, text: string) => void;
}) {
  const { t } = useTranslation();
  const labels = sel?.labels ?? [];
  const multi = !!q.multiSelect;
  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex items-start gap-2.5">
        <p className="flex-1 text-prose font-semibold leading-[1.4] text-foreground">
          {q.question}
        </p>
        <span className="flex shrink-0 items-center gap-1 rounded-sm border border-border bg-muted px-1.5 py-0.5 text-meta font-semibold tracking-wide text-muted-foreground">
          {multi ? (
            <SquareCheck className="h-2.5 w-2.5" />
          ) : (
            <CircleDot className="h-2.5 w-2.5" />
          )}
          {multi ? t("canonical.userAsk.multi") : t("canonical.userAsk.single")}
        </span>
      </div>
      <div className="flex flex-col gap-2">
        {q.options.map((opt, oIdx) => {
          const selected = labels.includes(opt.label);
          return (
            <button
              key={oIdx}
              type="button"
              disabled={locked}
              onClick={() => onToggle(qIdx, opt.label, multi)}
              className={cn(
                "flex w-full flex-col gap-1.5 rounded-md border px-3.5 py-3 text-left transition-colors",
                selected
                  ? "border-primary bg-primary-soft"
                  : "border-border bg-card hover:border-primary/40",
              )}
            >
              <div className="flex items-center gap-2.5">
                {multi ? (
                  selected ? (
                    <SquareCheck className="h-4 w-4 text-primary" />
                  ) : (
                    <Square className="h-4 w-4 text-muted-foreground/60" />
                  )
                ) : selected ? (
                  <CircleDot className="h-4 w-4 text-primary" />
                ) : (
                  <Circle className="h-4 w-4 text-muted-foreground/60" />
                )}
                <span
                  className={cn(
                    "text-aux font-semibold",
                    selected ? "text-primary-text" : "text-foreground",
                  )}
                >
                  {opt.label}
                </span>
              </div>
              {opt.description && (
                <p className="pl-[26px] text-xs text-muted-foreground">
                  {opt.description}
                </p>
              )}
            </button>
          );
        })}

        <div
          className={cn(
            "flex items-center gap-2.5 rounded-md border border-dashed px-3.5 py-2.5",
            labels.includes(OTHER_LABEL)
              ? "border-primary/60 bg-primary-soft"
              : "border-border-strong",
          )}
        >
          <PencilLine className="h-3.5 w-3.5 text-muted-foreground" />
          <Input
            type={q.isSecret ? "password" : "text"}
            disabled={locked}
            placeholder={t("canonical.userAsk.otherPlaceholder")}
            value={sel?.otherText ?? ""}
            onChange={(e) => {
              const text = e.target.value;
              onOther(qIdx, text);
              const has = labels.includes(OTHER_LABEL);
              if (text && !has) onToggle(qIdx, OTHER_LABEL, multi);
              if (!text && has) onToggle(qIdx, OTHER_LABEL, multi);
            }}
            className="h-7 border-0 bg-transparent p-0 text-sm focus-visible:ring-0"
          />
        </div>
      </div>
    </div>
  );
}
