// 编辑器打开或换绑设备时查一次凭据状态（spec「编辑器打开或切换绑定设备时查询一次
// 凭据状态」）。状态不能靠同步字段猜（决策 7）：这里只在设备门未拦时才发请求，
// 拦下的三种情形与查询失败都归零到 null，调用方据此回落到「未知」而不是编造。
import * as React from "react";

import type {
  BackendCredentialStatusView,
  EngineSettingsPorts,
} from "../ports";
import type { BackendType } from "../agent-backends-shared";

import type { CredentialDeviceGate } from "./credential-gate";

export function useBackendCredentialStatus(args: {
  type: BackendType;
  /** OpenClaw 后端已保存的 sync_id；未保存草稿时为空，查不了。 */
  syncId: string;
  /** Hermes 规范化后可查询的 URL；loopback / 还没填完时为空，查不了。 */
  hermesQueryUrl: string;
  deviceId: string;
  gate: CredentialDeviceGate;
  backendCredentialStatus?: EngineSettingsPorts["backendCredentialStatus"];
}): BackendCredentialStatusView | null {
  const {
    type,
    syncId,
    hermesQueryUrl,
    deviceId,
    gate,
    backendCredentialStatus,
  } = args;
  const [status, setStatus] =
    React.useState<BackendCredentialStatusView | null>(null);

  const identifier = type === "hermes" ? hermesQueryUrl : syncId;
  const queryable =
    gate === "" &&
    Boolean(backendCredentialStatus) &&
    (type === "hermes" || type === "openclaw") &&
    identifier !== "";

  React.useEffect(() => {
    if (!queryable) {
      setStatus(null);
      return;
    }
    let cancelled = false;
    void backendCredentialStatus!({
      type,
      syncId: type === "openclaw" ? syncId : undefined,
      hermesUrl: type === "hermes" ? hermesQueryUrl : undefined,
      deviceId,
    })
      .then((res) => {
        if (!cancelled) setStatus(res);
      })
      .catch(() => {
        if (!cancelled) setStatus(null);
      });
    return () => {
      cancelled = true;
    };
    // queryable 已经把 gate/backendCredentialStatus/type/identifier 折进真值里，
    // 但 effect 仍要在这些值本身变化时重跑（尤其是 deviceId：换绑设备要重查）。
  }, [
    queryable,
    backendCredentialStatus,
    type,
    syncId,
    hermesQueryUrl,
    deviceId,
  ]);

  return status;
}
