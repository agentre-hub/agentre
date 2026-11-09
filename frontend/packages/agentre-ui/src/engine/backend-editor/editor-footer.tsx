// 编辑器底栏：两条结果条（保存 / 测试各一条，互不覆盖），左侧「测试连接」在飞行中
// 换成「取消测试」，右侧取消与保存。
import { SendHorizontal, X } from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { Button } from "../../ui/button";
import { cn } from "../../lib/utils";
import type { FlashState } from "../agent-backends-shared";

import { TestResultPill } from "../agent-backends-badges";

export function BackendEditorFooter({
  saveResult,
  testResult,
  testing,
  submitting,
  syncingProvider,
  piAgentModelMissing,
  // 绑定设备未选择/离线/不在账号内时，测试连接（Hermes / OpenClaw 的凭据操作
  // 之一）同样不可用，且不发请求（spec「Editor behaviour on both hosts」）。
  credentialGateBlocked = false,
  // 整页档：取消与保存在页头（✕ · 标题 · 保存），底栏只剩结果条与一颗满宽、
  // 触屏尺寸的测试连接。不传时与弹窗档一致。
  actionsInHeader = false,
  submitDisabled,
  onTest,
  onCancelTest,
  onClose,
}: {
  saveResult: FlashState;
  testResult: FlashState;
  testing: boolean;
  submitting: boolean;
  syncingProvider: boolean;
  piAgentModelMissing: boolean;
  credentialGateBlocked?: boolean;
  actionsInHeader?: boolean;
  submitDisabled: boolean;
  onTest: () => void;
  onCancelTest: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  return (
    <>
      {saveResult ? <TestResultPill state={saveResult} /> : null}
      {testResult ? <TestResultPill state={testResult} /> : null}
      <div className="flex w-full items-center gap-2">
        {testing ? (
          <Button
            type="button"
            variant="outline"
            onClick={onCancelTest}
            className={cn(
              "gap-1.5 text-destructive-text",
              actionsInHeader && "h-11 flex-1 text-base",
            )}
          >
            <X className="size-3.5" aria-hidden="true" />
            {t("agentBackends.actions.cancelTest")}
          </Button>
        ) : (
          <Button
            type="button"
            variant="outline"
            disabled={
              submitting ||
              syncingProvider ||
              piAgentModelMissing ||
              credentialGateBlocked
            }
            onClick={onTest}
            className={cn(
              "gap-1.5",
              actionsInHeader && "h-11 flex-1 text-base",
            )}
          >
            <SendHorizontal className="size-3.5" aria-hidden="true" />
            {t("agentBackends.actions.test")}
          </Button>
        )}
        {actionsInHeader ? null : (
          <div className="ml-auto flex items-center gap-2">
            <Button
              type="button"
              variant="outline"
              onClick={onClose}
              disabled={submitting || syncingProvider}
            >
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={submitDisabled || syncingProvider}>
              {submitting ? t("common.saving") : t("common.save")}
            </Button>
          </div>
        )}
      </div>
    </>
  );
}
