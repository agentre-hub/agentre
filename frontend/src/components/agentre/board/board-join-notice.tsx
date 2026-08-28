import * as React from "react";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  SyncAcknowledgeBoardJoinNotice,
  SyncStatus,
} from "../../../../wailsjs/go/app/App";

/**
 * 「看板已并入同步组」那条**一次性**说明。
 *
 * 任务与标签这一轮才开始跟着账号走，用户此前建的每一条任务都会在下一次同步里出现在
 * 别的机器上 —— 这件事得先说一声。后端用 `Status.BoardJoinNoticePending` 记账，界面
 * 展示过一次就调 `SyncAcknowledgeBoardJoinNotice` 销掉它，不每次开看板都念一遍。
 *
 * 未登录 / 未开同步时 `Status()` 返回 `{enabled:false}`（不抛错），整条不出现。
 */
export function BoardJoinNotice() {
  const { t } = useTranslation();
  const [pending, setPending] = React.useState(false);

  React.useEffect(() => {
    let live = true;
    void SyncStatus()
      .then((status) => {
        if (live) setPending(Boolean(status?.boardJoinNoticePending));
      })
      .catch(() => {
        // 单机构建里绑定可能整个不存在：没有同步就没有这条说明。
      });
    return () => {
      live = false;
    };
  }, []);

  if (!pending) return null;

  return (
    <div
      data-testid="board-join-notice"
      className="flex shrink-0 items-center gap-2 border-b border-border bg-primary-soft px-5 py-2 text-2xs text-primary-text"
    >
      <span className="min-w-0 flex-1">{t("issues.syncJoined")}</span>
      <button
        type="button"
        aria-label={t("common.close")}
        onClick={() => {
          setPending(false);
          void SyncAcknowledgeBoardJoinNotice().catch(() => {
            // 销账失败无所谓：下次打开会再说一次，比把说明卡在屏幕上强。
          });
        }}
        className="cursor-pointer rounded-md p-1 transition-colors hover:bg-primary/10 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40"
      >
        <X className="size-3.5" aria-hidden="true" />
      </button>
    </div>
  );
}
