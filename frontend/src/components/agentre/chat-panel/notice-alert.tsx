import { TriangleAlert, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Alert, AlertDescription, Button } from "@agentre-hub/agentre-ui";

import type { ChatPanelNotice } from "./notice";

/** Inline notice (取代 window.alert)。
 *  error / info 两种态共用一个 slot，最多挂一条；右侧 × 关闭。
 *  info 用 default Alert 样式（中性），error 用 destructive。 */
function ChatPanelNoticeAlert({
  notice,
  onDismiss,
}: {
  notice: ChatPanelNotice;
  onDismiss: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Alert
      variant={notice.kind === "error" ? "destructive" : "default"}
      className="py-2 pr-2"
    >
      <TriangleAlert aria-hidden="true" />
      <AlertDescription className="flex min-w-0 items-start gap-2">
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="min-w-0 break-words text-xs leading-snug">
            {notice.text}
          </span>
          {notice.detail ? (
            <span
              data-testid="notice-detail"
              data-selectable-text="true"
              className="min-w-0 break-words font-mono text-2xs leading-snug opacity-80"
            >
              {notice.detail}
            </span>
          ) : null}
          {notice.actions ? (
            <div className="flex shrink-0 items-center gap-2 pt-1">
              <Button
                type="button"
                size="xs"
                variant="outline"
                onClick={notice.actions.retry}
              >
                {t("chatPanel.sendRetry.retry")}
              </Button>
              <Button
                type="button"
                size="xs"
                variant="ghost"
                onClick={notice.actions.discard}
              >
                {t("chatPanel.sendRetry.discard")}
              </Button>
            </div>
          ) : null}
        </div>
        <button
          type="button"
          aria-label={t("chatPanel.notice.close")}
          onClick={onDismiss}
          className="-mr-1 inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded-sm text-current opacity-70 transition-opacity hover:opacity-100"
        >
          <X className="size-3" aria-hidden="true" />
        </button>
      </AlertDescription>
    </Alert>
  );
}

export { ChatPanelNoticeAlert };
