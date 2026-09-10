// 跨路由 turn 落定后的善后:done / error / aborted / closed / steer_consumed 各自的收尾。
//
// store 在每轮结束时给该 sessionId 自增 doneTick。这里只关心「当前正在显示」的会话:抓最新
// 的 lastDoneEvent,reload 一次 useChatSession 把后端写好的最终 blocks(穿插顺序)拉回来,
// 再做 error 文案那类副作用。
//
// 两件事刻意不在这里做:
//   - MarkChatSessionRead 不在这里调 —— 由 chat-panel 那个 active-gated effect 在
//     reloadSession 拉到新的 session.lastMessageAt 之后自动触发(隐藏 tab active=false
//     不该被标已读)。
//   - closed 单独出现(没先来 done/error)不算 turn 结束:不 reload、也不动 errorText。
//
// 第一次 mount 时 doneTick=0,什么都不做(用 ref 跳过首次)。
import * as React from "react";

import type { ChatMessage } from "@/hooks/use-chat-session";
import { useSessionStatusStore } from "@/stores/session-status-store";

import {
  applySteerConsumed,
  applyStreamError,
  upsertMessage,
} from "./stream-view";

export function useTurnSettledCleanup({
  sessionId,
  setMessages,
  reloadSession,
  onSidebarShouldReload,
}: {
  sessionId: number;
  setMessages: React.Dispatch<React.SetStateAction<ChatMessage[]>>;
  reloadSession: () => Promise<void>;
  onSidebarShouldReload?: () => void;
}): void {
  // 这条 store 订阅只喂本 hook:谁读 doneTick / lastDoneEvent 谁去订阅(组件原先在
  // 自己身上订阅,抽出来之后那份订阅就没有第二个读者了)。
  //   每次 turn 结束(done/error/aborted/closed/steer_consumed)bumpDone 自增 doneTick,
  //   下面的 lastSeenDoneTickRef effect 据此触发 reload + 副作用。
  const liveStatus = useSessionStatusStore((s) =>
    sessionId ? (s.statuses.get(sessionId) ?? null) : null,
  );
  const doneTick = liveStatus?.doneTick ?? 0;
  const lastDoneEvent = liveStatus?.lastDoneEvent ?? null;

  const lastSeenDoneTickRef = React.useRef(doneTick);
  React.useEffect(() => {
    if (!sessionId) return;
    if (doneTick === lastSeenDoneTickRef.current) return;
    lastSeenDoneTickRef.current = doneTick;
    const ev = lastDoneEvent;
    if (!ev) return;
    if (ev.kind === "steer_consumed") {
      setMessages((prev) => applySteerConsumed(prev, ev));
      void reloadSession();
      onSidebarShouldReload?.();
    } else if (ev.kind === "done") {
      // 后端在发 done 前已经 chat_repo.Message().Update,reload 拿到最终顺序。
      //
      // 但不能只靠 reload:finishStream 是同步的,liveDelta / liveBlocks 当场清零,
      // 而 messages 里那条 assistant 还是发送时插的空占位(blocks: [])——
      // 中间那段 LoadChatSession 往返里,最后一轮的正文整段消失、行数塌陷,
      // 响应回来才重新长出来。done 事件本身就带着最终 assistant 消息
      // (chat_svc 的 `ChatStreamEvent{Kind: StreamDone, Message: final}`),
      // 先同步落表,空窗就没了。reload 仍要发 —— 本轮可能还改了别的行
      // (user 消息、subagent 子行、审批块),done 只覆盖 assistant 那一条。
      if (ev.message) {
        setMessages((prev) => upsertMessage(prev, ev.message!));
      }
      void reloadSession();
      onSidebarShouldReload?.();
    } else if (ev.kind === "error") {
      // 错误路径:后端同样 Update 过 assistant.errorText,但有可能 final message 已附带
      // ev.message。两条路都靠 reload 把最新落库状态拿回来;再补 errorText 落到 UI。
      if (ev.message) {
        setMessages((prev) => upsertMessage(prev, ev.message!));
      } else if (ev.error) {
        setMessages((prev) => applyStreamError(prev, ev.error));
      }
      void reloadSession();
      onSidebarShouldReload?.();
    } else if (ev.kind === "aborted") {
      // 用户主动「停止」：后端已经把 partial 内容写入 DB 且 errorText 为空。
      // 走和 done 一样的路径:事件自带 partial 消息就先同步落表(同样是为了不
      // 在等 reload 的这段里把已经生成的内容闪没),再 reload 兜其余的行；
      // 不调 MarkRead（abort 不是「用户已读完」语义）。
      if (ev.message) {
        setMessages((prev) => upsertMessage(prev, ev.message!));
      }
      void reloadSession();
      onSidebarShouldReload?.();
    } else if (ev.kind === "closed") {
      // closed 单独出现(没先来 done/error)通常意味着 wails 端被关掉,不算 turn 结束,
      // 不主动 reload 也不动 errorText —— 与旧版行为对齐。
    }
  }, [
    doneTick,
    lastDoneEvent,
    onSidebarShouldReload,
    reloadSession,
    sessionId,
    setMessages,
  ]);
}
