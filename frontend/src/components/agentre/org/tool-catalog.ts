import type { TFunction } from "i18next";

import type { department_svc } from "../../../../wailsjs/go/models";

// 已知需审批的工具(后端写操作走审批门)。新增工具时在此登记，
// 真正的 per-tool 审批元数据未来应由后端提供(本期前端已知集合)。
export const APPROVAL_TOOLS = new Set(["org", "hook"]);

// ToolListItem 是工具清单的一行：图标由消费侧按 key 取，这里只给文案与两个状态位。
// approval 刻意不等于「这个工具属于 APPROVAL_TOOLS」——它是「这一行现在要不要标
// 需审批」：未授权的工具还没有任何写操作可言，先标出来只是噪音（规格：需审批只在
// 已授权时出现）。
export type ToolListItem = {
  key: string;
  name: string;
  description: string;
  granted: boolean;
  approval: boolean;
};

// buildToolList 把可用工具 key 列表拍成清单：已授权的排在前面，组内保持传入顺序
// （后端给的 key 顺序）——排序只由「授权与否」这一件事决定，重排不会因为文案语言
// 不同而在两个语种下长得不一样。
export function buildToolList(
  keys: string[],
  agentTools: department_svc.AgentToolDTO[],
  t: TFunction,
): ToolListItem[] {
  const enabledByKey = new Map(agentTools.map((tl) => [tl.key, tl.enabled]));
  const items = keys.map((key) => {
    const granted = enabledByKey.get(key) ?? false;
    return {
      key,
      name: t(`org.agent.tools.names.${key}`),
      description: t(`org.agent.tools.descriptions.${key}`),
      granted,
      approval: granted && APPROVAL_TOOLS.has(key),
    };
  });
  return [
    ...items.filter((it) => it.granted),
    ...items.filter((it) => !it.granted),
  ];
}
