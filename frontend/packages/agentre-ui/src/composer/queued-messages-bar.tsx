import {
  Hourglass,
  Loader2,
  Lock,
  Trash2,
  TriangleAlert,
  Undo2,
  X,
} from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import type { QueuedItem } from "./steer-queue";

type Props = {
  queued: QueuedItem[];
  /** 用户点单条 chip 上的 X（删除，不放回输入框）。父组件负责调 CancelQueuedChatMessage 并同步本地 state。 */
  onCancel: (id: string) => void;
  /**
   * 用户点单条 chip 上的「撤回到输入框」。宿主负责撤回、对端确认移除之后把原文追加进
   * 输入框。可选：不接的宿主只有删除那一颗，与从前一样。
   */
  onWithdraw?: (id: string) => void;
  /** 输入框正在编辑一条历史消息：撤回下来的字会被当成对它的修改，所以先不让撤。 */
  withdrawDisabled?: boolean;
  /** 用户点 header「清空」。父组件负责调 CancelQueuedChatMessage({queuedId: ""})。 */
  onClearAll: () => void;
  /** 回合收尾时还没被 AI 消费、被暂存的排队条目。宿主只在它属于当前这条会话时
   *  传入；空或 null 不渲染。与 queued 互斥（轮末清空了队列），但组件不依赖这个
   *  前提，有 dropped 时优先渲染丢弃横幅。 */
  dropped?: QueuedItem[] | null;
  /** 点「恢复为草稿」。这些字回哪儿由宿主定（桌面端放回队列，控制台回输入框草稿）。 */
  onRestoreDropped?: () => void;
  /** 点「丢弃」。宿主负责把暂存的那一份扔掉。 */
  onDiscardDropped?: () => void;
  /** 触屏档：撤回 / 删除各 44px 命中区、间距 8px，撤回禁用的原因以可见文字出现在条内。不传与今天一致。 */
  inputMode?: "touch";
  /**
   * 轮末没送到、已被宿主放回输入框的插话：画成一行「N 条插话没送到，已放回输入框 · 清掉」。
   * 何时显示由宿主定；count 为 0 或不传不渲染。点「清掉」走 onClear（宿主撤掉那次追加）。
   */
  undeliveredNotice?: { count: number; onClear: () => void } | null;
};

// chip 上撤回 / 删除两颗按钮同一个样式。禁用态（撤回在途、编辑中）不再响应悬停。
const chipButtonClass = cn(
  "inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
  "disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:bg-transparent disabled:hover:text-muted-foreground",
);

// 触屏档按钮：44px 命中区，图标仍是 12px。
const touchChipButtonClass = chipButtonClass.replace("size-5", "size-11");

// QueuedMessagesBar 显示这条会话此刻还没被 AI 取走的排队消息。
// 挂在 ChatComposer 内的 card 顶部（编辑模式 banner 同位，走 topSlot），样式上用
// muted 区分于 input 主区，让用户一眼看到「这是缓冲，不是正常输入」。
//
// 空列表时 render null —— 这是组件契约：宿主不必判空，QueuedMessagesBar
// 自己决定可见性。这样 ChatComposer 的 topSlot 一直传 <QueuedMessagesBar/>
// 也不会留空 DOM。
//
// 状态迁移不在这里：入队 / 认领句柄 / 消费 / 撤回都归 ./steer-queue 那份纯归约，
// 两个宿主共用。这里只画它算出来的那一份。
export function QueuedMessagesBar({
  queued,
  onCancel,
  onWithdraw,
  withdrawDisabled = false,
  onClearAll,
  dropped,
  onRestoreDropped,
  onDiscardDropped,
  inputMode,
  undeliveredNotice,
}: Props) {
  const { t } = useUiTranslation();
  const touch = inputMode === "touch";
  const buttonClass = touch ? touchChipButtonClass : chipButtonClass;
  const notice =
    undeliveredNotice && undeliveredNotice.count > 0 ? (
      <div
        data-testid="undelivered-notice"
        className="flex items-center gap-2 bg-muted px-3 py-2 text-2xs text-muted-foreground"
      >
        <span className="min-w-0">
          {t("queuedMessages.undelivered.notice", {
            count: undeliveredNotice.count,
          })}
        </span>
        <span aria-hidden="true">·</span>
        <button
          type="button"
          onClick={undeliveredNotice.onClear}
          className={cn(
            "cursor-pointer text-foreground underline-offset-2 hover:underline",
            touch && "inline-flex min-h-11 items-center",
          )}
        >
          {t("queuedMessages.undelivered.clear")}
        </button>
      </div>
    ) : null;
  // 丢弃横幅优先:轮末暂存时队列已被清空,正常条目的 queued.length===0 判空
  // 会把它整个藏掉 —— 这里在判空之前先看有没有被暂存的丢弃条目。
  if (dropped && dropped.length > 0) {
    return (
      <div
        role="alert"
        aria-label={t("queuedMessages.dropped.title", {
          count: dropped.length,
        })}
        className="flex flex-col gap-1.5 bg-destructive-soft px-3 py-2"
      >
        <div className="flex items-center gap-2">
          <TriangleAlert
            className="size-3 shrink-0 text-status-error"
            aria-hidden="true"
          />
          <span className="text-2xs font-semibold text-destructive-text">
            {t("queuedMessages.dropped.title", {
              count: dropped.length,
            })}
          </span>
          <div className="min-w-0 flex-1" />
          <button
            type="button"
            aria-label={t("queuedMessages.dropped.discard")}
            onClick={onDiscardDropped}
            className="inline-flex h-6 cursor-pointer items-center gap-1 rounded-sm bg-destructive/10 px-2 text-2xs font-medium text-destructive-text transition-colors hover:bg-destructive/20 hover:text-destructive-text"
          >
            {t("queuedMessages.dropped.discard")}
          </button>
          <button
            type="button"
            aria-label={t("queuedMessages.dropped.restore")}
            onClick={onRestoreDropped}
            className="inline-flex h-6 cursor-pointer items-center gap-1 rounded-sm bg-button-fill px-2 text-2xs font-medium text-foreground transition-colors hover:bg-button-fill-hover hover:text-foreground"
          >
            {t("queuedMessages.dropped.restore")}
          </button>
        </div>
        <ul className="flex flex-col gap-1">
          {dropped.map((q) => (
            <li
              key={q.id}
              className="flex items-center gap-2 rounded-sm bg-card px-2 py-1"
              title={q.text}
            >
              <TriangleAlert
                className="size-3 shrink-0 text-status-error"
                aria-hidden="true"
              />
              <span
                data-selectable-text="true"
                className="min-w-0 flex-1 truncate text-xs text-destructive-text"
              >
                {q.text}
              </span>
            </li>
          ))}
        </ul>
      </div>
    );
  }
  if (queued.length === 0) return notice;
  const anyCancellable = queued.some((q) => q.cancellable);

  const showWithdrawReason = touch && !!onWithdraw && withdrawDisabled;

  return (
    <>
      {notice}
      <div
        role="region"
        aria-label={t("queuedMessages.aria")}
        className="flex flex-col gap-1.5 bg-muted px-3 py-2"
      >
        <div className="flex items-center gap-2">
          <Hourglass
            className="size-3 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
          <span className="text-2xs font-semibold text-foreground">
            {t("queuedMessages.count", { count: queued.length })}
          </span>
          <span className="text-2xs text-muted-foreground">
            {anyCancellable
              ? t("queuedMessages.insertAfterAI")
              : t("queuedMessages.noCancelSupport")}
          </span>
          <div className="min-w-0 flex-1" />
          <button
            type="button"
            disabled={!anyCancellable}
            aria-label={t("queuedMessages.clearAria")}
            title={
              anyCancellable
                ? t("queuedMessages.clearTitle")
                : t("queuedMessages.noCancelSupportTitle")
            }
            onClick={onClearAll}
            className={cn(
              "inline-flex h-6 cursor-pointer items-center gap-1 rounded-sm bg-button-fill px-2 text-2xs font-medium transition-colors",
              "hover:bg-button-fill-hover hover:text-foreground",
              "disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:bg-button-fill",
            )}
          >
            <Trash2 className="size-3" aria-hidden="true" />
            {t("queuedMessages.clear")}
          </button>
        </div>
        <ul className="flex flex-col gap-1">
          {queued.map((q) => (
            <li
              key={q.id}
              className="flex items-center gap-2 rounded-sm bg-card px-2 py-1"
              title={q.text}
            >
              <Hourglass
                className="size-3 shrink-0 text-muted-foreground"
                aria-hidden="true"
              />
              <span
                data-selectable-text="true"
                className="min-w-0 flex-1 truncate text-xs text-foreground"
              >
                {q.text}
              </span>
              {q.cancellable ? (
                <span
                  className={cn(
                    "flex shrink-0 items-center",
                    touch ? "gap-2" : "gap-0.5",
                  )}
                >
                  {onWithdraw ? (
                    <button
                      type="button"
                      aria-label={t("queuedMessages.withdrawOne")}
                      title={
                        withdrawDisabled
                          ? t("queuedMessages.withdrawDisabledEditing")
                          : t("queuedMessages.withdrawOneTitle")
                      }
                      disabled={q.withdrawing || withdrawDisabled}
                      onClick={() => onWithdraw(q.id)}
                      className={buttonClass}
                    >
                      {q.withdrawing ? (
                        <Loader2
                          className="size-3 animate-spin motion-reduce:animate-none"
                          aria-hidden="true"
                        />
                      ) : (
                        <Undo2 className="size-3" aria-hidden="true" />
                      )}
                    </button>
                  ) : null}
                  <button
                    type="button"
                    aria-label={t("queuedMessages.cancelOne")}
                    title={t("queuedMessages.cancelOneTitle")}
                    disabled={q.withdrawing}
                    onClick={() => onCancel(q.id)}
                    className={buttonClass}
                  >
                    <X className="size-3" aria-hidden="true" />
                  </button>
                </span>
              ) : (
                <span
                  aria-label={t("queuedMessages.notCancellable")}
                  // 这一条自己那句说明优先：撤回被拒时它讲的是**这一条**为什么撤不动。
                  title={q.note ?? t("queuedMessages.notCancellableTitle")}
                  className="inline-flex size-5 shrink-0 items-center justify-center text-muted-foreground"
                >
                  <Lock className="size-3" aria-hidden="true" />
                </span>
              )}
            </li>
          ))}
        </ul>
        {showWithdrawReason ? (
          <p
            data-testid="withdraw-disabled-reason"
            className="text-2xs text-muted-foreground"
          >
            {t("queuedMessages.withdrawDisabledEditing")}
          </p>
        ) : null}
      </div>
    </>
  );
}
