import * as React from "react";
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
import { Button } from "../../../ui/button";
import { Input } from "../../../ui/input";
import {
  SegmentedControl,
  SegmentedControlItem,
} from "../../../ui/segmented-control";
import {
  TranscriptCard,
  TranscriptCardBody,
  TranscriptPill,
} from "../../transcript-card";
import {
  clearTranscriptDraftState,
  loadTranscriptDraftState,
  saveTranscriptDraftState,
} from "../../chat-panel-scroll-state";
import { cn } from "../../../lib/utils";
import { useCollapsible } from "../../../hooks/use-collapsible";
import { useTranscriptBooleanState } from "../../transcript-ui-state";
import { useUiTranslation } from "../../../i18n";

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
  const { t } = useUiTranslation();
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
        className="flex w-full items-center gap-2 px-3.5 py-2.5 text-left transition-colors hover:bg-accent/60 hover:on-accent"
      >
        <ChevronDown
          aria-hidden="true"
          className={cn(
            "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform duration-150 ease-out",
            collapsed && "-rotate-90",
          )}
        />
        <MessageSquareQuote className="h-3.5 w-3.5 shrink-0 text-primary" />
        <span className="font-mono text-xs font-semibold text-primary-text">
          user_ask
        </span>
        {headerLabel && (
          <>
            <span className="font-mono text-xs text-muted-foreground">·</span>
            <span className="rounded-sm bg-primary/10 px-1.5 py-0.5 text-meta font-medium text-primary-text">
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
                  allAnswered={isAnswered && !isSkipped}
                  onSelect={setActiveQIdx}
                />
              )}
              <TranscriptCardBody className="flex flex-col gap-3">
                {payload.questions[activeQIdx] && (
                  <QuestionGroup
                    q={payload.questions[activeQIdx]}
                    qIdx={activeQIdx}
                    sel={selections[activeQIdx]}
                    locked={isLocked}
                    answered={isAnswered && !isSkipped}
                    onToggle={toggleOption}
                    onOther={setOtherText}
                  />
                )}
                {error && (
                  <div className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive-text">
                    {error}
                  </div>
                )}
              </TranscriptCardBody>

              {!isLocked && (
                <div className="flex items-center gap-2 px-3.5 py-2.5">
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
  const { t } = useUiTranslation();
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
  allAnswered,
  onSelect,
}: {
  questions: AskQuestionDTO[];
  activeIdx: number;
  selections: Selection[];
  allAnswered: boolean;
  onSelect: (idx: number) => void;
}) {
  return (
    <div className="px-3.5 pb-1">
      <SegmentedControl
        value={String(activeIdx)}
        onValueChange={(v) => onSelect(Number(v))}
        className="max-w-full overflow-x-auto"
      >
        {questions.map((q, idx) => {
          const active = idx === activeIdx;
          // 已作答的卡里秘密题的答复不带内容，不能靠选中项判断。
          const answered =
            allAnswered || (selections[idx]?.labels.length ?? 0) > 0;
          const label = q.header ? `Q${idx + 1} · ${q.header}` : `Q${idx + 1}`;
          return (
            <SegmentedControlItem key={idx} value={String(idx)}>
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
            </SegmentedControlItem>
          );
        })}
      </SegmentedControl>
    </div>
  );
}

function QuestionGroup({
  q,
  qIdx,
  sel,
  locked,
  answered,
  onToggle,
  onOther,
}: {
  q: AskQuestionDTO;
  qIdx: number;
  sel: Selection | undefined;
  locked: boolean;
  answered: boolean;
  onToggle: (qIdx: number, label: string, multi: boolean) => void;
  onOther: (qIdx: number, text: string) => void;
}) {
  const { t } = useUiTranslation();
  const labels = sel?.labels ?? [];
  const multi = !!q.multiSelect;
  // 「其他」由后端逐题决定；无选项的题文本输入是唯一答法，始终保留。
  const offersText = !q.disallowOther || q.options.length === 0;
  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex items-start gap-2.5">
        <p className="flex-1 text-prose font-semibold leading-[1.4] text-foreground">
          {q.question}
        </p>
        <span className="flex shrink-0 items-center gap-1 rounded-sm bg-muted px-1.5 py-0.5 text-meta font-semibold tracking-wide text-muted-foreground">
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
                "flex w-full flex-col gap-1.5 rounded-md px-3.5 py-3 text-left transition-colors",
                selected
                  ? "bg-primary-soft"
                  : "bg-button-fill hover:bg-button-fill-hover",
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

        {q.isSecret && answered ? (
          // 秘密答案只留「已作答」，内容既不落档也不回显。
          <div className="flex items-center gap-2.5 rounded-md bg-button-fill px-3.5 py-2.5 text-sm text-muted-foreground">
            <PencilLine className="h-3.5 w-3.5" />
            {t("canonical.userAsk.secretAnswered")}
          </div>
        ) : offersText ? (
          <div
            className={cn(
              "flex items-center gap-2.5 rounded-md px-3.5 py-2.5",
              labels.includes(OTHER_LABEL)
                ? "bg-primary-soft"
                : "bg-button-fill",
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
              variant="inline"
              className="h-7 p-0 text-sm"
            />
          </div>
        ) : null}
      </div>
    </div>
  );
}
