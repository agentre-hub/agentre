/**
 * `!` 开头那条消息按下发送后的落地：交给宿主执行，并按执行结果决定留不留痕。
 *
 * 顺序是有讲究的：先**预留**一个 lastUsedAt 再执行，拿到真实执行作用域之后才
 * `record`；执行没发生（宿主没接、抛了、返回空作用域）就把预留还回去。反过来
 * 「执行完再取时间戳」会让并发的两条命令在历史里的先后与实际相反。
 *
 * 宿主没有历史能力时 `history` 为 null：命令照常提交，只是不留痕。
 */
import type { LocalCommandHistoryAccess } from "./local-command-history/access";
import type { LocalCommandSubmitHandler } from "./types";

export interface SubmitLocalCommandArgs {
  command: string;
  history: LocalCommandHistoryAccess | null;
  onCommandSubmit: LocalCommandSubmitHandler | undefined;
}

export function submitLocalCommand({
  command,
  history,
  onCommandSubmit,
}: SubmitLocalCommandArgs): void {
  const warnSubmissionFailure = (error: unknown) => {
    console.warn("[chat-input] local command submission failed", error);
  };
  let submittedAt: number | undefined;
  try {
    submittedAt = history?.reserveLastUsedAt();
  } catch (error) {
    console.warn(
      "[chat-input] failed to reserve local command history order",
      error,
    );
  }
  const releaseHistoryReservation = () => {
    if (submittedAt === undefined) return;
    history?.releaseLastUsedAt(submittedAt);
  };
  try {
    const executionScope = onCommandSubmit?.(command);
    if (!executionScope) {
      releaseHistoryReservation();
    } else {
      void Promise.resolve(executionScope)
        .then((scope) => {
          if (!scope || submittedAt === undefined) return;
          try {
            history?.record(scope, command, submittedAt);
          } catch (error) {
            console.warn(
              "[chat-input] failed to record local command history",
              error,
            );
          }
        })
        .catch(warnSubmissionFailure)
        .finally(releaseHistoryReservation);
    }
  } catch (error) {
    warnSubmissionFailure(error);
    releaseHistoryReservation();
  }
}
