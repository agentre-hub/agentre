import * as React from "react";
import {
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
import { Spinner } from "../../../ui/spinner";
import { StatusMark } from "../../../ui/status-mark";
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
import { isAskExpiredError } from "../../ask-expired";
import { FailureDetail, failureDetailText } from "../../failure-detail";

import type { CanonicalCardProps } from "../props";
import type {
  AskQuestionDTO,
  AskAnswerDTO,
  CanonicalDTO,
  UserAskDTO,
} from "../types";
import {
  useDecisionCardEnv,
  useHasDecisionCardEnv,
} from "../tool-permission/decision-card-env";
import { isTimeoutFailure } from "../tool-permission/use-tool-permission-decision";
import { useSubmitAnswer } from "./use-submit-answer";

// 触屏命中区 ≥ 44px。
const TOUCH_TARGET = "min-h-11";

// OTHER_LABEL 与后端 canonical.OtherAnswerLabel 对齐。
const OTHER_LABEL = "__other__";

type Selection = { labels: string[]; otherText: string };
type UserAskDraftState = { activeQIdx: number; selections: Selection[] };
// 卡内的一条错误。qIdx 有值时它属于那一题(漏答),只在那一题下显示;detail 是
// 提交失败的机器原话,只进折叠的详情。
type CardError = {
  message: string;
  qIdx?: number;
  detail?: string;
  /** 提交失败、可以原样再来一次(宿主级反馈下给「重试」)。 */
  retryable?: boolean;
};

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

export type UserAskCardProps = CanonicalCardProps & {
  /** 触屏档:标题本地化为「Agent 在问你」,选项与按钮 ≥44px,不画回车键帽。不传保持桌面外观。 */
  touch?: boolean;
  /** 执行端连接未就绪:提交 / 跳过禁用并写明原因。选项仍可先选。 */
  connecting?: boolean;
};

// UserAskCard 渲染 canonical.user.ask —— 结构化问答(claudecode AskUserQuestion /
// codex request_user_input)。读 toolBlock.canonical.userAsk。
export const UserAskCard: React.FC<UserAskCardProps> = ({
  toolBlock,
  sessionId,
  uiStateKey,
  tabStateKey,
  touch: touchProp,
  connecting: connectingProp,
}) => {
  const { t } = useUiTranslation();
  const env = useDecisionCardEnv();
  const touch = touchProp ?? env.touch;
  const connecting = connectingProp ?? env.connecting;
  // 宿主级反馈(点下的那颗单独转圈、失败「重试」、超时「没收到确认」):宿主传了
  // connecting 或包了 DecisionCardEnvProvider 才打开。都没有:保持桌面原样。
  const hasEnv = useHasDecisionCardEnv();
  const managed = connectingProp !== undefined || hasEnv;
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
  // 正在提交的是「跳过」还是「提交」;上一次的动作给「重试」重放。
  const [pendingSkip, setPendingSkip] = React.useState<boolean | null>(null);
  const lastSkipRef = React.useRef(false);
  // 提交时运行时答复「提问已不存在」——只有这种失败锁卡。
  const [expiredOnSubmit, setExpiredOnSubmit] = React.useState(false);
  const [error, setError] = React.useState<CardError | null>(null);
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
  const isExpired = !!payload?.expired || expiredOnSubmit;
  const isLocked = isAnswered || isSkipped || isExpired || submitting;
  // 宿主级反馈下提交中脚部不收起:点下的那颗按钮单独转圈。
  const showFooter = managed
    ? !(isAnswered || isSkipped || isExpired)
    : !isLocked;

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
        // 漏答:切到第一道缺答的题,错误挂在那一题下。
        for (let i = 0; i < (payload.questions ?? []).length; i++) {
          const sel = selections[i];
          const missing =
            !sel || sel.labels.length === 0
              ? "canonical.userAsk.errors.optionRequired"
              : sel.labels.includes(OTHER_LABEL) && !sel.otherText.trim()
                ? "canonical.userAsk.errors.otherRequired"
                : null;
          if (missing) {
            setActiveQIdx(i);
            setError({ message: t(missing), qIdx: i });
            return;
          }
        }
      }
      lastSkipRef.current = skipped;
      setPendingSkip(skipped);
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
      } catch (err) {
        if (isAskExpiredError(err)) {
          setExpiredOnSubmit(true);
          setError({ message: t("canonical.userAsk.errors.expired") });
        } else if (managed && isTimeoutFailure(err)) {
          // 超时:没收到确认,回答可能已经生效——不说「没成功」。
          setError({
            message: t("canonical.userAsk.errors.timedOut"),
            retryable: true,
          });
        } else {
          // 其余失败可重试:不锁卡,选择原样留着。
          setError({
            message: t("canonical.userAsk.errors.submitFailed"),
            detail: failureDetailText(err),
            retryable: managed,
          });
        }
      } finally {
        setPendingSkip(null);
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
      managed,
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
          {touch ? t("canonical.userAsk.askingTitle") : "user_ask"}
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
                    touch={touch}
                    onToggle={toggleOption}
                    onOther={setOtherText}
                  />
                )}
                {error &&
                  // 连接断了时上一次的提交失败已经过时:只留「正在连接」一条。
                  !(managed && connecting && error.retryable) &&
                  (error.qIdx === undefined || error.qIdx === activeQIdx) && (
                    <div
                      role="alert"
                      className="flex flex-col gap-1 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive-text"
                    >
                      <p>{error.message}</p>
                      <FailureDetail detail={error.detail} />
                      {managed && error.retryable ? (
                        <Button
                          type="button"
                          size="sm"
                          variant="outline"
                          className={cn("self-start", touch && TOUCH_TARGET)}
                          disabled={submitting}
                          onClick={() => void handleSubmit(lastSkipRef.current)}
                        >
                          {t("common.retry")}
                        </Button>
                      ) : null}
                    </div>
                  )}
              </TranscriptCardBody>

              {showFooter && connecting && (
                <p
                  data-testid="user-ask-connecting"
                  className="flex items-center gap-2 px-3.5 pt-2.5 text-aux text-muted-foreground"
                >
                  <Spinner className="size-3.5" />
                  {t("canonical.userAsk.connecting")}
                </p>
              )}
              {showFooter && (
                <div className="flex items-center gap-2 px-3.5 py-2.5">
                  <div className="flex-1" />
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    className={touch ? TOUCH_TARGET : undefined}
                    disabled={submitting || connecting}
                    onClick={() => void handleSubmit(true)}
                  >
                    {managed && pendingSkip === true ? (
                      <Spinner className="size-4" />
                    ) : null}
                    {t("common.skip")}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    disabled={submitting || connecting}
                    onClick={() => void handleSubmit(false)}
                    className={cn("gap-1.5", touch && TOUCH_TARGET)}
                  >
                    {managed && pendingSkip === false ? (
                      <Spinner className="size-4" />
                    ) : null}
                    {t("canonical.userAsk.submit")}
                    {touch ? null : <CornerDownLeft className="h-3 w-3" />}
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
  if (expired) return <StatusMark state="invalidated" />;
  if (skipped) return <StatusMark state="skipped" />;
  if (answered) return <StatusMark state="answered" />;
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
  touch,
  onToggle,
  onOther,
}: {
  q: AskQuestionDTO;
  qIdx: number;
  sel: Selection | undefined;
  locked: boolean;
  answered: boolean;
  touch: boolean;
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
                touch && TOUCH_TARGET,
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
              touch && TOUCH_TARGET,
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
              // 移动端 16px（iOS 聚焦小于 16px 的输入会放大视口），md 及以上 14px。
              className={cn("p-0 text-base md:text-sm", touch ? "h-11" : "h-7")}
            />
          </div>
        ) : null}
      </div>
    </div>
  );
}
