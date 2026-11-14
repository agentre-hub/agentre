import { Crown } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

/**
 * 部门负责人记号：一枚皇冠，「负责人」是它的可访问名与悬停提示。组织树的部门行与桌面端
 * 成员栏的负责人徽标共用这一份；`className` 落在皇冠上（尺寸、身份色）。
 */
function LeaderMark({ className }: { className?: string }) {
  const { t } = useUiTranslation();
  const name = t("org.index.leader");
  return (
    <span
      role="img"
      aria-label={name}
      title={name}
      className="inline-flex shrink-0 items-center"
    >
      <Crown className={cn("size-3", className)} aria-hidden="true" />
    </span>
  );
}

export { LeaderMark };
