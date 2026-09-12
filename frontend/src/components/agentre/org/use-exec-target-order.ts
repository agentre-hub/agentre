import * as React from "react";

import type { department_svc } from "../../../../wailsjs/go/models";

import type { ExecTargetRow } from "./exec-target-list";
import { useExecTargetAvailability } from "./use-exec-target-availability";

/** 账号级集合里的一档：技能挂在它身上，顺序不由它决定。 */
type ExecTargetSet = {
  agentBackendId: number;
  skills: department_svc.AgentSkillDTO[];
};

/**
 * 执行目标区那一份列表的取数与合并：**本端生效顺序**来自后端
 * ListExecTargetAvailability，**技能**来自账号级集合，两者按 agentBackendId 合起来
 * 才是行要渲染的东西 —— 合并只做这一处。
 */
export function useExecTargetOrder(
  agentId: number,
  execTargets: ExecTargetSet[],
) {
  // 本端生效顺序（R14 解析后的顺序，含覆盖 / 无覆盖时本机自己提前）来自后端
  // ListExecTargetAvailability 的返回数组顺序，它就是执行目标区唯一那份列表。
  const deviceTargetsKey = React.useMemo(
    () =>
      execTargets
        .map((t) => t.agentBackendId)
        .slice()
        .sort((a, b) => a - b)
        .join(","),
    [execTargets],
  );
  const availability = useExecTargetAvailability(agentId, deviceTargetsKey);
  // null = 这一轮读还没落定（顺序还在路上）；数组 = 已落定的本端顺序，读失败时是空
  // 数组。两者必须分开，否则「读完了但没拿到」会被当成「还在路上」，列表永远停在骨架。
  const [deviceTargets, setDeviceTargets] = React.useState<
    ExecTargetRow[] | null
  >(null);
  React.useEffect(() => {
    if (availability.orderedTargets.length > 0) {
      setDeviceTargets(availability.orderedTargets);
    } else if (availability.settled) {
      setDeviceTargets([]);
    }
  }, [availability.orderedTargets, availability.settled]);

  // 顺序数据还没到达：渲染骨架而不是空态卡片（真正的空态是「这个 Agent 没有任何
  // 执行目标」，此时 execTargets 也是空的，下面这个判断自然为假）。
  const orderPending = execTargets.length > 0 && deviceTargets === null;

  // 顺序读完了却没拿到（读失败）时列表回落到账号级集合自己的顺序：增删/更换与顺序
  // 无关，任何状态下都得可用（规格「增删恒可用」），把列表钉死在骨架上等于把它们
  // 一起锁掉。用户少看到的只是「本机档自动提前」那一下重排，能做的事一件不少。
  //
  // 技能挂在账号级集合那一份上（execTargets），顺序来自本端解析，两者按
  // agentBackendId 合起来才是行要渲染的东西 —— 合并只做这一处。
  const skillsByBackendId = React.useMemo(() => {
    const m = new Map<number, department_svc.AgentSkillDTO[]>();
    for (const t of execTargets) m.set(t.agentBackendId, t.skills);
    return m;
  }, [execTargets]);
  const listTargets: ExecTargetRow[] = (
    deviceTargets && deviceTargets.length > 0
      ? deviceTargets
      : execTargets.map((t) => ({ agentBackendId: t.agentBackendId }))
  ).map((row) => ({
    agentBackendId: row.agentBackendId,
    skills: skillsByBackendId.get(row.agentBackendId) ?? [],
  }));

  return { availability, setDeviceTargets, orderPending, listTargets };
}
