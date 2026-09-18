import * as React from "react";

import {
  GetBackendCapabilities,
  GetSessionCapabilities,
} from "@/../wailsjs/go/app/App";

import { Capabilities, type PermissionModeMeta } from "./types";

// useCapabilities 是 session / backend 两条能力查询的共同实现：只有 RPC 与「什么
// 时候该拉」这两件事不同，响应整形与失败兜底完全一致。
//
// 业务用法（两个具名 hook 各自把它接到自己的 RPC 上）：
//   const { caps } = useSessionCapabilities(sessionId);
//   const { caps } = useBackendCapabilities(newSessionAgent?.backendType);
type CapabilityResponse = {
  capabilities?: string[] | null;
  permissionModeMeta?: {
    allowedModes?: string[] | null;
    defaultMode?: string | null;
    switchableDuringTurn?: boolean | null;
    order?: string[] | null;
  } | null;
};

function useCapabilities(
  key: string | number | null | undefined,
  load: () => Promise<CapabilityResponse>,
  errorLabel: string,
) {
  const [caps, setCaps] = React.useState<Capabilities | null>(null);

  React.useEffect(() => {
    if (!key) {
      setCaps(null);
      return;
    }
    let cancelled = false;
    void load()
      .then((resp) => {
        if (cancelled) return;
        const meta: PermissionModeMeta = {
          allowedModes: resp?.permissionModeMeta?.allowedModes ?? [],
          defaultMode: resp?.permissionModeMeta?.defaultMode ?? "",
          switchableDuringTurn:
            resp?.permissionModeMeta?.switchableDuringTurn ?? false,
          order: resp?.permissionModeMeta?.order ?? [],
        };
        setCaps(new Capabilities(new Set(resp?.capabilities ?? []), meta));
      })
      .catch((e: unknown) => {
        if (!cancelled) {
          console.error(`[capability] ${errorLabel}`, e);
          setCaps(null);
        }
      });
    return () => {
      cancelled = true;
    };
    // load 每次渲染都是新闭包；重新拉取的触发条件是 key（sessionId / backendType）。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  return { caps };
}

// useSessionCapabilities 拉 chat session 对应 runtime 的能力矩阵 + permission
// mode metadata。sessionId 改变时重新拉;<=0 时返回 null(没选会话)。
export function useSessionCapabilities(sessionId: number | undefined | null) {
  const key = sessionId && sessionId > 0 ? sessionId : null;
  return useCapabilities(
    key,
    () => GetSessionCapabilities({ sessionId } as never),
    "load failed",
  );
}

// useBackendCapabilities 按 backend type 拉 runtime 能力矩阵 + permission mode
// metadata。给「新对话还没建 session」场景用 — 已有 session 走
// useSessionCapabilities,语义一致(响应形状复用)。
export function useBackendCapabilities(backendType: string | undefined | null) {
  return useCapabilities(
    backendType || null,
    () => GetBackendCapabilities({ backendType } as never),
    "backend caps load failed",
  );
}
