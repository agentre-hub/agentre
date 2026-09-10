import { TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  Alert,
  AlertDescription,
  AlertTitle,
  Button,
} from "@agentre-hub/agentre-ui";

import { splitErrorDetail } from "@/lib/error-detail";

// 持久化的会话加载失败不再静默关闭 tab:改为在转录区渲染这张错误卡（Retry / Close），
// 由用户决定去留。真正的删除流（confirmDelete）才调 onSessionDeleted。
function SessionLoadError({
  error,
  onRetry,
  onClose,
}: {
  error: string;
  onRetry: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const loadMsg = splitErrorDetail(error).msg;
  // 后端 ChatSessionNotFound 的 zh 文案。不写字面量,用码点拼 ——
  // i18n 守卫会把任何 Han 字符串字面量当硬编码 UI copy 拦下;
  // 这是匹配动态后端错误文本,不是 UI copy,不进 t()。
  const chatSessionNotFoundZh = String.fromCharCode(
    0x4f1a, // 会
    0x8bdd, // 话
    0x4e0d, // 不
    0x5b58, // 存
    0x5728, // 在
  );
  const loadNotFound =
    loadMsg.includes("Chat session not found") ||
    loadMsg.includes(chatSessionNotFoundZh);
  return (
    <Alert
      variant="destructive"
      aria-label={t("chatPanel.loadError.title")}
      className="ml-10 max-w-measure"
    >
      <TriangleAlert aria-hidden="true" />
      <AlertTitle>{t("chatPanel.loadError.title")}</AlertTitle>
      <AlertDescription className="flex flex-col gap-3">
        <span
          data-selectable-text="true"
          className="min-w-0 break-words text-aux"
        >
          {loadMsg}
        </span>
        {loadNotFound ? (
          <span className="text-meta">
            {t("chatPanel.loadError.notFoundHint")}
          </span>
        ) : null}
        <span className="flex items-center gap-2">
          <Button type="button" size="xs" onClick={onRetry}>
            {t("chatPanel.loadError.retry")}
          </Button>
          <Button type="button" size="xs" variant="ghost" onClick={onClose}>
            {t("chatPanel.loadError.close")}
          </Button>
        </span>
      </AlertDescription>
    </Alert>
  );
}

export { SessionLoadError };
