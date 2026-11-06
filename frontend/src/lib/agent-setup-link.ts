// 「Agent 缺配置」去哪修：一律落到组织架构，并带上落点意图（路由 state）。
//
// 三个入口共用这一处（规格 2026-11-05 S2）：不能对话引导弹窗、LLM 供应商页的
// 「待补齐」清单、对话空态。组织页读意图走 readOrgLinkState，契约因此只写在这里。
import type { NavigateFunction } from "react-router-dom";

/** 组织页记住选中行的键（org/use-org-index-view.ts 读写同一个）。 */
export const ORG_SELECTED_STORAGE_KEY = "agentre.orgTree.selected";

const NEEDS_SETUP_FILTER = "needs-setup";

export type OrgLinkSelection = { kind: "agent" | "department"; id: number };

export type OrgLinkState = {
  orgSelection?: OrgLinkSelection;
  orgFilter?: typeof NEEDS_SETUP_FILTER;
};

/** 直达某个 Agent：组织页挂载前先写选中键，已挂载时靠 state 里的 orgSelection。 */
export function openAgentSetup(navigate: NavigateFunction, agentId: number) {
  const orgSelection: OrgLinkSelection = { kind: "agent", id: agentId };
  localStorage.setItem(ORG_SELECTED_STORAGE_KEY, JSON.stringify(orgSelection));
  navigate("/org", { state: { orgSelection } satisfies OrgLinkState });
}

/** 一个缺口直达它；多个缺口进组织架构并预选「待配置」，不替用户挑。 */
export function openAgentGaps(
  navigate: NavigateFunction,
  agentIds: readonly number[],
) {
  if (agentIds.length === 1) {
    openAgentSetup(navigate, agentIds[0]);
    return;
  }
  if (agentIds.length > 1) {
    navigate("/org", {
      state: { orgFilter: NEEDS_SETUP_FILTER } satisfies OrgLinkState,
    });
  }
}

export function readOrgLinkState(state: unknown): {
  selection: OrgLinkSelection | null;
  needsSetup: boolean;
} {
  const link = (state ?? {}) as OrgLinkState;
  const selection = link.orgSelection;
  return {
    selection: selection?.kind && selection.id > 0 ? selection : null,
    needsSetup: link.orgFilter === NEEDS_SETUP_FILTER,
  };
}
