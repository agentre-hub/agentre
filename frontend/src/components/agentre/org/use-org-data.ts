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

  const reload = React.useCallback(async () => {
    try {
      const [res, backendsRes] = await Promise.all([
        LoadOrg(),
        ListAgentBackends(),
      ]);
      setState({
        loading: false,
        error: null,
        departments: res.departments ?? [],
        agents: res.agents ?? [],
        backends: backendsRes.items ?? [],
        availableTools: res.availableTools ?? [],
      });
    } catch (err) {
      setState((s) => ({ ...s, loading: false, error: messageOf(err) }));
    }
  }, []);

  React.useEffect(() => {
    setState((s) => ({ ...s, loading: true }));
    void reload();
  }, [reload]);

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

  const mutate = React.useCallback(
    async <T>(fn: () => Promise<T>): Promise<T | null> => {
      inFlight.current += 1;
      try {
        const result = await fn();
        return result;
      } catch (err) {
        setState((s) => ({ ...s, error: messageOf(err) }));
        return null;
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
      );
    },
    reorderDepartments: (parentId: number, orderedIds: number[]) => {
      setState((s) => ({
        ...s,
        departments: applyDepartmentOrder(s.departments, parentId, orderedIds),
      }));
      return mutate(() => ReorderDepartments({ parentId, orderedIds }));
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
