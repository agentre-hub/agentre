// frontend/src/components/agentre/remote-devices/use-device-upgrade.ts
//
// 桌面端这一侧的取数适配：状态机本身归共享包
// (`useAgentredUpgrade` —— 两端的态与迁移只该有一份实现),这里只把它接到 Wails
// 绑定上:发起受理调用走 RemoteDeviceUpgrade,轮询期间的版本读本机缓存的远端快照。
//
// 渠道不再由这里选择或传入:RemoteDeviceUpgrade 已经不接受渠道参数,桌面端自己
// 的构建渠道由 remote_device_svc 在 Go 侧解析(决策 10 —— 设置删除后这是唯一的
// 渠道来源)。

import { useMemo } from "react";

import {
  useAgentredUpgrade,
  type AgentredUpgrade,
  type AgentredUpgradePhase,
} from "@agentre-hub/agentre-ui";

import {
  RemoteDeviceGet,
  RemoteDeviceUpgrade,
} from "../../../../wailsjs/go/app/App";

/** 桌面端沿用的名字;取值与迁移由共享包定义。 */
export type UpgradePhase = AgentredUpgradePhase;

export function useDeviceUpgrade(
  deviceId: number,
  currentVersion: string,
): AgentredUpgrade {
  const ports = useMemo(
    () => ({
      requestUpgrade: async (force: boolean) => {
        const result = await RemoteDeviceUpgrade(deviceId, force);
        return {
          accepted: result.accepted,
          rejectReason: result.rejectReason,
          message: result.message,
          activeTurns: result.activeTurns,
          targetVersion: result.targetVersion,
        };
      },
      readVersion: async () => (await RemoteDeviceGet(deviceId))?.daemonVersion,
    }),
    [deviceId],
  );
  return useAgentredUpgrade(currentVersion, ports);
}
