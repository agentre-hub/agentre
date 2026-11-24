import { create } from "zustand";
import { toast } from "sonner";

import i18n from "@/i18n";

import { IssueLinkSession } from "../../wailsjs/go/app/App";

/**
 * 会话 ↔ 看板任务的关联「变了」这一信号。
 *
 * 关联在会话页（首条消息发出时）建立，看板页另一棵树里要刷新卡片下的会话列表：
 * 两处只共享这一个递增版本号，看板订阅它重取，不互相持有对方的状态。
 */
type State = {
  revision: number;
  bump: () => void;
};

export const useIssueLinksStore = create<State>((set) => ({
  revision: 0,
  bump: () => set((state) => ({ revision: state.revision + 1 })),
}));

/**
 * 把刚建立的会话关联到任务。会话已经建成，关联失败只告知、不回滚会话。
 * 无论成败都通知看板重取：失败时它重取到的仍是真实状态。
 */
export async function linkSessionToIssue(
  sessionId: number,
  issueId: number,
): Promise<void> {
  try {
    await IssueLinkSession(sessionId, issueId);
  } catch (cause) {
    console.error("[issue-links] link failed", cause);
    toast.error(i18n.t("issues.errors.linkFailed"));
  } finally {
    useIssueLinksStore.getState().bump();
  }
}
