import { ChevronUp, Loader2 } from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";

import { Button } from "@agentre-hub/agentre-ui";

/**
 * EarlierMessagesLoader 是转录顶部「取回更早正文」的入口。
 *
 * 读路径是「元数据全量 + 块按需取」(spec 2026-08-27 决策 6):打开会话只下发最近一个
 * 窗口的完整正文,更早的消息手上只有元数据与派生视图点名的那几类块。它们不进转录
 * (半份正文渲染出来就是缺了工具结果的假转录),这条入口负责把它们取回来。
 *
 * 与 CompactHistoryFold 的区别:那条折叠的是**已经在本地**、只是默认收起的压缩前
 * 历史;这条要取的正文还在库里。两者可以同时出现。
 */
export function EarlierMessagesLoader({
  loading = false,
  onLoad,
}: {
  loading?: boolean;
  onLoad: () => void;
}): React.ReactElement {
  const { t } = useTranslation();

  return (
    <div className="flex w-full max-w-measure justify-center py-2">
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="gap-2 text-aux text-muted-foreground"
        disabled={loading}
        onClick={onLoad}
      >
        {loading ? (
          <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
        ) : (
          <ChevronUp className="h-3.5 w-3.5" aria-hidden="true" />
        )}
        <span>
          {loading
            ? t("transcriptWindow.loadingEarlier")
            : t("transcriptWindow.loadEarlier")}
        </span>
      </Button>
    </div>
  );
}
