import { Bot, Check, Network, Webhook, Wrench } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { buildOrgToolList, type OrgAgentTool } from "./tool-catalog";

// 工具清单每一行行首的那个图标：清单是「一行只有图标、名字、需审批、一句话能力、
// 动作按钮」，图标只用来在扫读时区分行，未登记的 key 落到通用的扳手上。
const TOOL_ICONS: Record<string, typeof Network> = {
  org: Network,
  subagent: Bot,
  hook: Webhook,
};

/**
 * 行为栏里的工具清单：**清单以外没有第二处入口** —— 授权与撤销都在这一行上完成，
 * 已授权的排在前面、底色标出、行首一个 ✓。
 *
 * 两处刻意的取舍（规格决策 9）：
 *   · 动作是**文字按钮**（`授权` / `已授权`）而不是图标钮或开关 —— 这几项是把权限
 *     授给一个 AI、其中两项还带审批门，按一下是有意为之，拨一下是顺手；
 *   · ✓ **不替换**工具图标而是并排 —— 替换掉之后，已授权的那几行反倒认不出是哪个
 *     工具，而「这个 Agent 现在能干什么」正是要在那几行上读的。
 */
export type OrgToolListProps = {
  /** 可授权的工具 key（后端给的顺序）。 */
  toolKeys: string[];
  /** 这个 Agent 现在的授权状态。 */
  agentTools: OrgAgentTool[];
  onToggleGrant: (key: string) => void;
};

export function OrgToolList({
  toolKeys,
  agentTools,
  onToggleGrant,
}: OrgToolListProps) {
  const { t } = useUiTranslation();
  const items = buildOrgToolList(toolKeys, agentTools, t);
  const grantedCount = items.filter((it) => it.granted).length;

  return (
    <div className="min-w-0 space-y-2" data-slot="agent-section-tools">
      <div className="flex items-center gap-2">
        <h4 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
          {t("org.agent.tools.sectionTitle")}
        </h4>
        <div className="flex-1" />
        {/* 带分母：只说「已授权 2」读不出还剩几项没授，而「还能授什么」正是这份
            清单要回答的另一半。 */}
        <span className="font-mono text-2xs text-muted-foreground">
          {t("org.agent.tools.grantedCount", {
            granted: grantedCount,
            total: items.length,
          })}
        </span>
      </div>
      {items.length === 0 ? (
        <p className="text-2xs text-muted-foreground">
          {t("org.agent.tools.empty")}
        </p>
      ) : (
        <ul
          aria-label={t("org.agent.tools.title")}
          className="flex min-w-0 flex-col overflow-hidden rounded-md border border-border"
        >
          {items.map((item, index) => {
            const Icon = TOOL_ICONS[item.key] ?? Wrench;
            return (
              <li
                key={item.key}
                data-granted={item.granted}
                className={cn(
                  "flex min-w-0 items-center gap-2 px-2.5 py-1.5",
                  index > 0 && "border-t border-border",
                  item.granted ? "bg-primary-soft" : "bg-input-bg",
                )}
              >
                {/* ✓ 占一个固定宽度的位：未授权时留空而不是塌陷，否则两组行的
                    图标与名字对不上一条竖线。 */}
                <span
                  className="w-3 shrink-0 text-primary-text"
                  aria-hidden="true"
                >
                  {item.granted && (
                    <Check
                      className="size-3"
                      data-slot="org-tool-granted-mark"
                    />
                  )}
                </span>
                <span
                  className={cn(
                    "shrink-0",
                    item.granted
                      ? "text-primary-text"
                      : "text-muted-foreground",
                  )}
                  aria-hidden="true"
                >
                  <Icon className="size-3.5" data-slot="org-tool-icon" />
                </span>
                <span
                  className={cn(
                    "shrink-0 whitespace-nowrap text-xs font-medium",
                    !item.granted && "text-muted-foreground",
                  )}
                >
                  {item.name}
                </span>
                {item.approval && (
                  <span className="inline-flex shrink-0 items-center rounded-sm bg-status-waiting-bg px-1.5 font-mono text-2xs text-status-waiting">
                    {t("org.agent.tools.approval")}
                  </span>
                )}
                {/* 一句话能力与名字同一行：清单要能一眼扫完，掉到第二行就是每项
                    占两行的说明书。窄了就截断，全文在 title 上。 */}
                <span
                  className="min-w-0 flex-1 truncate text-2xs text-muted-foreground"
                  title={item.description}
                >
                  {item.description}
                </span>
                <button
                  type="button"
                  aria-label={t(
                    item.granted
                      ? "org.agent.tools.revokeNamed"
                      : "org.agent.tools.grantNamed",
                    { name: item.name },
                  )}
                  title={t(
                    item.granted
                      ? "org.agent.tools.revokeNamed"
                      : "org.agent.tools.grantNamed",
                    { name: item.name },
                  )}
                  onClick={() => onToggleGrant(item.key)}
                  className={cn(
                    "shrink-0 rounded-sm border px-2 py-0.5 text-2xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
                    item.granted
                      ? "border-status-running bg-status-running-bg text-status-running"
                      : "border-border text-muted-foreground hover:bg-accent hover:text-foreground",
                  )}
                >
                  {t(
                    item.granted
                      ? "org.agent.tools.granted"
                      : "org.agent.tools.grant",
                  )}
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
