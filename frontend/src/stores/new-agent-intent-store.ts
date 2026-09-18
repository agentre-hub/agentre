import { create } from "zustand";
import { subscribeWithSelector } from "zustand/middleware";

/**
 * 「打开新建 agent 弹窗」这条**树外意图**：命令面板 / 会话索引页发出去，组织页接住。
 *
 * 与 `command-palette-store` 同一口径的 zustand store。这里多一枚 `revision`：
 * 订阅方只认**每一次 request**，而 consume 清掉 pending 时不该反过来叫醒它们 ——
 * 所以订阅按 `revision`（只有 request 会 +1）过滤，而不是订阅整个 state。
 * `pending` 只管「这条意图还没被消费」，无订阅者时它就地留着，挂载时补消费。
 */
type NewAgentIntentState = {
  pending: boolean;
  /** 每次 request +1。订阅方拿它当「又来了一条」的信号，consume 不动它。 */
  revision: number;
  request: () => void;
  /** 消费这条意图；返回 false 表示本来就没有。同一次意图只可能被消费一次。 */
  consume: () => boolean;
};

export const useNewAgentIntentStore = create<NewAgentIntentState>()(
  subscribeWithSelector((set, get) => ({
    pending: false,
    revision: 0,
    request: () =>
      set((state) => ({ pending: true, revision: state.revision + 1 })),
    consume: () => {
      if (!get().pending) return false;
      set({ pending: false });
      return true;
    },
  })),
);
