// 编辑器底栏：两条结果条（保存 / 测试各一条，互不覆盖），左侧「测试连接」在飞行中
// 换成「取消测试」，右侧取消与保存。
import { SendHorizontal, X } from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { Button } from "../../ui/button";
import type { FlashState } from "../agent-backends-shared";

import { TestResultPill } from "../agent-backends-badges";

export function BackendEditorFooter({
  saveResult,
  testResult,
  testing,
  submitting,
  syncingProvider,
  piAgentModelMissing,
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
            className="gap-1.5 text-status-error"
          >
            <X className="size-3.5" aria-hidden="true" />
            {t("agentBackends.actions.cancelTest")}
          </Button>
        ) : (
          <Button
            type="button"
            variant="outline"
            disabled={submitting || syncingProvider || piAgentModelMissing}
            onClick={onTest}
            className="gap-1.5"
          >
            <SendHorizontal className="size-3.5" aria-hidden="true" />
            {t("agentBackends.actions.test")}
          </Button>
        )}
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
      </div>
    </>
  );
}
