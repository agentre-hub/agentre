import * as React from "react";

import { ListAgentExecTargetAvailability } from "@/../wailsjs/go/app/App";
import type { exec_target_svc } from "@/../wailsjs/go/models";

// useExecTargetAvailability 逐档判定一个 Agent 的执行目标列表可用性（R15），供
// 组织架构页展示每档的「当前生效 / 在线 / 离线 / 未配对 / 需指定 LLM Provider…」
// 徽标。projectID 固定传 0——Agent 详情页不绑定任何会话/项目，不受「该机器上有没有
// 配这个项目的路径」这一项约束（那条只在 R15a 的会话内改选场景生效）。
//
// 返回一个按 agentBackendId 索引的 Map，而不是原始顺序数组：Agent 详情页里的执行
// 目标列表可能刚被本地拖拽重排、还没保存完成，这份数据仍然是「保存前最后一次成功
// 读取」的快照——按 id 查找让重排（不改变 backend 集合）不需要等一轮保存往返就能
// 复用已有的可用性判定，调用方只在「新增/删除了哪个 backend」时才需要拿到新数据
// （用 targetsKey 控制何时重新拉取）。
//
// R14：后端按解析后的顺序返回（本端覆盖 / 无覆盖时本机自己提前），所以响应数组
// 本身**就是本端当前生效顺序**——orderedTargets 原样交回数组顺序，它就是执行目标区
// 那一份列表。hasOverride 标注是否处于本端覆盖，服务端照常返回，但界面不消费它：
// 界面上没有「账号默认顺序」这个概念，「有没有覆盖」也就没有可讲的区别。
export function useExecTargetAvailability(agentId: number, targetsKey: string) {
  const [byBackendId, setByBackendId] = React.useState<
    Map<number, exec_target_svc.ExecTargetAvailabilityView>
  >(new Map());
  const [orderedTargets, setOrderedTargets] = React.useState<
    { agentBackendId: number }[]
  >([]);
  const [hasOverride, setHasOverride] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  // settled 在第一次请求**落定**（成功或失败）后为真。它回答的是「这一轮读完了吗」，
  // 而 orderedTargets 回答「读到了什么」——两者必须分开：读失败时 orderedTargets 照样
  // 是空数组，调用方若只看它就会把「读完了但没拿到」误当成「还在路上」。
  const [settled, setSettled] = React.useState(false);
  // 只有最新一次请求可以写状态：增删执行目标会连着触发多次拉取，先发的那次晚返回时
  // 不能把新一批判定盖回旧的（徽标会一直停在删掉那一档还在的快照上）。卸载时把代次
  // 推进一格，在飞的请求回来后不再写已卸载组件的状态。
  const reqRef = React.useRef(0);

  const reload = React.useCallback(async () => {
    const req = ++reqRef.current;
    if (!agentId) {
      setByBackendId(new Map());
      setOrderedTargets([]);
      setHasOverride(false);
      setSettled(true);
      return;
    }
    setLoading(true);
    try {
      const resp = await ListAgentExecTargetAvailability(agentId, 0);
      if (req !== reqRef.current) return;
      const rows = resp ?? [];
      setByBackendId(new Map(rows.map((s) => [s.agentBackendId, s])));
      setOrderedTargets(
        rows.map((s) => ({ agentBackendId: s.agentBackendId })),
      );
      setHasOverride(rows.length > 0 && rows[0].hasOverride);
    } catch (e) {
      if (req !== reqRef.current) return;
      console.error("[org] exec target availability load failed", e);
    } finally {
      if (req === reqRef.current) {
        setLoading(false);
        setSettled(true);
      }
    }
    // targetsKey 只用来判断"这一批 backend id 是否变了"，值本身不进请求体。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentId, targetsKey]);

  React.useEffect(() => {
    void reload();
  }, [reload]);

  React.useEffect(
    () => () => {
      reqRef.current++;
    },
    [],
  );

  return { byBackendId, orderedTargets, hasOverride, loading, settled, reload };
}
