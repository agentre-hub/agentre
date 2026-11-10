import * as React from "react";

import i18n from "@/i18n";

import {
  CreateAgent,
  CreateDepartment,
  DeleteAgent,
  DeleteAgentAvatar,
  DeleteDepartment,
  ListAgentBackends,
  LoadOrg,
  MoveAgent,
  MoveDepartment,
  ReorderAgents,
  ReorderDepartments,
  UpdateAgent,
  UpdateDepartment,
  UploadAgentAvatar,
} from "../../../../wailsjs/go/app/App";
import type {
  agent_backend_svc,
  agent_svc,
  department_svc,
} from "../../../../wailsjs/go/models";
import { EventsOn } from "../../../../wailsjs/runtime/runtime";

import type { OrgAgent, OrgDepartment } from "./types";
import { applyAgentOrder, applyDepartmentOrder } from "./reorder";

type State = {
  loading: boolean;
  /**
   * 只表示「从没加载出来过」：首次加载失败的原话（页面只放进 `title`）。加载成功过
   * 之后的任何失败 —— 后台重拉、写入 —— 都不进这里，页面内容原样留着。
   */
  error: string | null;
  departments: OrgDepartment[];
  agents: OrgAgent[];
  backends: agent_backend_svc.BackendItem[];
  availableTools: string[];
};

const initialState: State = {
  loading: true,
  error: null,
  departments: [],
  agents: [],
  backends: [],
  availableTools: [],
};

export function useOrgData() {
  const [state, setState] = React.useState<State>(initialState);
  const inFlight = React.useRef(0);
  const loaded = React.useRef(false);

  const reload = React.useCallback(async () => {
    try {
      const [res, backendsRes] = await Promise.all([
        LoadOrg(),
        ListAgentBackends(),
      ]);
      loaded.current = true;
      setState({
        loading: false,
        error: null,
        departments: res.departments ?? [],
        agents: res.agents ?? [],
        backends: backendsRes.items ?? [],
        availableTools: res.availableTools ?? [],
      });
    } catch (err) {
      // 已经有内容时，一次重拉失败不值得把整页换掉：留着上一份，下一次事件 / 写入
      // 会再拉。
      if (loaded.current) return;
      setState((s) => ({ ...s, loading: false, error: messageOf(err) }));
    }
  }, []);

  // 首次加载，也是失败态上「重试」走的那条路：回到加载占位再拉一次。
  const load = React.useCallback(() => {
    setState((s) => ({ ...s, loading: true }));
    void reload();
  }, [reload]);

  React.useEffect(() => {
    load();
  }, [load]);

  // config:changed（本机写入）/ sync:applied（多端同步落地）到达时就地重拉——用不
  // 带 loading 置位的 `reload`，页面不会因为一次后台刷新回退到加载占位或提示条
  // （docs/specs/2026-09-22-agrctl-resource-management.md「Real-time refresh」）。
  // 本页自己的写入还没全部落定时不重拉：那次重拉会拿回不含后续写入的数据，盖掉乐观
  // 更新；最后一个写入结束时 `mutate` 自己会重拉一次。
  React.useEffect(() => {
    const reloadWhenSettled = () => {
      if (inFlight.current > 0) return;
      void reload();
    };
    const offConfigChanged = EventsOn("config:changed", reloadWhenSettled);
    const offSyncApplied = EventsOn("sync:applied", reloadWhenSettled);
    return () => {
      offConfigChanged();
      offSyncApplied();
    };
  }, [reload]);

  // 写失败原样抛回调用方：弹窗、自动保存、拖拽各有各的说法，这里替它们说只能是把
  // 整页换成一句原话。成败都重拉一次，页面以库里的为准。
  const mutate = React.useCallback(
    async <T>(fn: () => Promise<T>): Promise<T> => {
      inFlight.current += 1;
      try {
        return await fn();
      } finally {
        inFlight.current -= 1;
        if (inFlight.current === 0) {
          void reload();
        }
      }
    },
    [reload],
  );

  return {
    ...state,
    reload,
    retryLoad: load,
    createDepartment: (req: department_svc.CreateDepartmentRequest) =>
      mutate(() => CreateDepartment(req)),
    updateDepartment: (req: department_svc.UpdateDepartmentRequest) =>
      mutate(() => UpdateDepartment(req)),
    moveDepartment: (req: department_svc.MoveDepartmentRequest) =>
      mutate(() => MoveDepartment(req)),
    deleteDepartment: (req: department_svc.DeleteDepartmentRequest) =>
      mutate(() => DeleteDepartment(req)),
    createAgent: (req: agent_svc.CreateAgentRequest) =>
      mutate(() => CreateAgent(req)),
    updateAgent: (req: agent_svc.UpdateAgentRequest) =>
      mutate(() => UpdateAgent(req)),
    moveAgent: (req: agent_svc.MoveAgentRequest) =>
      mutate(() => MoveAgent(req)),
    deleteAgent: (req: agent_svc.DeleteAgentRequest) =>
      mutate(() => DeleteAgent(req)),
    uploadAgentAvatar: (req: agent_svc.UploadAvatarRequest) =>
      mutate(() => UploadAgentAvatar(req)),
    deleteAgentAvatar: (req: agent_svc.DeleteAvatarRequest) =>
      mutate(() => DeleteAgentAvatar(req)),
    reorderAgents: (
      departmentId: number,
      parentAgentId: number,
      orderedIds: number[],
    ) => {
      const before = state.agents;
      setState((s) => ({
        ...s,
        agents: applyAgentOrder(
          s.agents,
          departmentId,
          parentAgentId,
          orderedIds,
        ),
      }));
      return mutate(() =>
        ReorderAgents({ departmentId, parentAgentId, orderedIds }),
      ).catch((err: unknown) => {
        // 乐观重排没落库：先退回拖之前的顺序，不等那次可能同样失败的重拉。
        setState((s) => ({ ...s, agents: before }));
        throw err;
      });
    },
    reorderDepartments: (parentId: number, orderedIds: number[]) => {
      const before = state.departments;
      setState((s) => ({
        ...s,
        departments: applyDepartmentOrder(s.departments, parentId, orderedIds),
      }));
      return mutate(() => ReorderDepartments({ parentId, orderedIds })).catch(
        (err: unknown) => {
          setState((s) => ({ ...s, departments: before }));
          throw err;
        },
      );
    },
  };
}

function messageOf(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "string") return err;
  try {
    return JSON.stringify(err);
  } catch {
    return i18n.t("common.operationFailed");
  }
}
