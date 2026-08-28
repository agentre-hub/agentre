import { ChevronDown, History } from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";

import { Button } from "@agentre-hub/agentre-ui";

// CompactHistoryFold 渲染 transcript 顶部"折叠的压缩前历史"提示条。
// 点击按钮把状态委托回父组件 (ChatTranscript) 切换 expanded,展开后显示全部历史。
//
// count 是被折叠的消息条数(用户视角的"几条历史"),不含 boundary 当前消息。
export function CompactHistoryFold({
  count,
  onExpand,
}: {
  count: number;
  onExpand: () => void;
}): React.ReactElement {
  const { t } = useTranslation();

  return (
    <div className="flex w-full max-w-measure justify-center py-2">
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="gap-2 text-aux text-muted-foreground"
        onClick={onExpand}
      >
        <History className="h-3.5 w-3.5" aria-hidden="true" />
        <span>{t("compactHistory.viewPrevious", { count })}</span>
        <ChevronDown className="h-3.5 w-3.5" aria-hidden="true" />
      </Button>
    </div>
  );
}
