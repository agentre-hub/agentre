import { Bot, Check, Network, Plus, Webhook, Wrench, X } from "lucide-react";

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
        <span className="font-mono text-2xs text-muted-foreground">
          {t("org.agent.tools.enabledCount", { count: grantedCount })}
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
                  "flex min-w-0 items-start gap-2 px-2.5 py-2",
                  index > 0 && "border-t border-border",
                  item.granted ? "bg-primary-soft" : "bg-input-bg",
                )}
              >
                <span
                  className={cn(
                    "mt-0.5 shrink-0",
                    item.granted
                      ? "text-primary-text"
                      : "text-muted-foreground",
                  )}
                  aria-hidden="true"
                >
                  {item.granted ? (
                    <Check className="size-3.5" />
                  ) : (
                    <Icon className="size-3.5" />
                  )}
                </span>
                <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                  <span className="flex min-w-0 flex-wrap items-center gap-1.5">
                    <span className="truncate text-xs font-semibold">
                      {item.name}
                    </span>
                    {item.approval && (
                      <span className="inline-flex shrink-0 items-center rounded-sm bg-status-waiting-bg px-1.5 py-0.5 font-mono text-2xs text-status-waiting">
                        {t("org.agent.tools.approval")}
                      </span>
                    )}
                  </span>
                  <span className="min-w-0 break-words text-2xs text-muted-foreground">
                    {item.description}
                  </span>
                </div>
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
                  className="mt-0.5 shrink-0 rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                >
                  {item.granted ? (
                    <X className="size-3.5" />
                  ) : (
                    <Plus className="size-3.5" />
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
