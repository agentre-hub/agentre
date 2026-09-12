// 「这个后端在哪台机器上跑」的全部状态：配对设备行、本机指纹、账号设备清单，
// 以及由它们推出的下拉选项、当前选中值与显示名。远端与否也在这里定案 ——
// 编辑器其余部分只读结论，不再自己拼设备身份。
import * as React from "react";

import {
  accountDeviceOptions,
  type DeviceOption,
} from "../agent-backends-utils";
import {
  deviceSelectValue,
  pairedDeviceSelectValue,
  resolveExecutionDevice,
} from "../device-identity";
import type { EngineSettingsBridge } from "../port-bridge";
import type { AccountDeviceView } from "../ports";
import type { Translate } from "../agent-backends-shared";

import type { DeviceView, EditorState } from "./editor-types";

// remote_device_watcher_svc 的在线态推送通道（与 session-exec-target / 设备面板同一条）。
const REMOTE_DEVICE_STATE_EVENT = "remote.device.state";

export const LOCAL_DEVICE_SELECT_VALUE = "__local_device__";

export type BackendDevices = ReturnType<typeof useBackendDevices>;

export function useBackendDevices(args: {
  stateKind: EditorState["kind"];
  hasLocalDevice: boolean;
  initialDeviceId: string;
  bridge: Pick<
    EngineSettingsBridge,
    | "EventsOn"
    | "RemoteDeviceFingerprint"
    | "RemoteDeviceList"
    | "ServerListDevices"
  >;
  t: Translate;
}) {
  const { stateKind, hasLocalDevice, t } = args;
  const {
    EventsOn,
    RemoteDeviceFingerprint,
    RemoteDeviceList,
    ServerListDevices,
  } = args.bridge;

  const [deviceId, setDeviceId] = React.useState<string>(args.initialDeviceId);
  const [devices, setDevices] = React.useState<DeviceView[]>([]);
  const [localFingerprint, setLocalFingerprint] = React.useState("");
  const [accountDevices, setAccountDevices] = React.useState<
    AccountDeviceView[]
  >([]);

  // Fetch paired remote devices when the dialog opens (or re-opens).
  //
  // 顺序是有意的，不是可以并发的两个读：账号设备的收编发生在 ServerListDevices
  // **内部**（app.ServerListDevices → AdoptAccountDevices 写 paired_agentreds）。
  // 并发时 RemoteDeviceList 大概率先返回，这一次就读不到刚收编的那一行 ——
  // 用户得关掉弹窗重开才看得见那台机器。所以先让收编跑完，再读配对行。
  // 指纹与账号清单之间没有依赖，仍可并发。
  React.useEffect(() => {
    if (stateKind === "closed") return;
    let cancelled = false;
    void (async () => {
      try {
        const [fingerprint, rowsFromAccount] = await Promise.all([
          RemoteDeviceFingerprint(),
          ServerListDevices().catch(() => [] as AccountDeviceView[]),
        ]);
        const rows = await RemoteDeviceList();
        if (cancelled) return;
        setDevices((rows ?? []) as unknown as DeviceView[]);
        setLocalFingerprint(fingerprint ?? "");
        setAccountDevices(rowsFromAccount ?? []);
      } catch {
        if (cancelled) return;
        setDevices([]);
        setLocalFingerprint("");
        setAccountDevices([]);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [RemoteDeviceFingerprint, RemoteDeviceList, ServerListDevices, stateKind]);

  // 运行设备下拉按 online 禁用选项，而在线态在弹窗开着的时候会翻转：刚收编的那一行
  // watcher 才开始拨号，那一刻 online=false。不订阅这条既有推送，那个灰掉的选项要
  // 等到用户关掉弹窗重开才会变可选。只改在线态，不重拉清单 —— 行集合没变。
  React.useEffect(() => {
    if (stateKind === "closed") return;
    const off = EventsOn(REMOTE_DEVICE_STATE_EVENT, (payload: unknown) => {
      const ev = payload as {
        id: number;
        name: string;
        online: boolean;
      };
      setDevices((prev) =>
        prev.map((d) =>
          d.id === ev.id
            ? { ...d, name: ev.name || d.name, online: ev.online }
            : d,
        ),
      );
    });
    return () => off?.();
  }, [EventsOn, stateKind]);

  // 远端执行时以目标 daemon 目录为可运行事实源（task 6 决策 12）：拉一次该设备的
  // Provider/Model 目录 + 能力位，传给 Picker 做远端门控（desktop 独有的行禁用、
  // 旧 daemon 禁用 fixed-model）。daemon 离线时目录为空 → 未验证的 fixed-model 无法保存。
  const executionDevice = React.useMemo(
    () => resolveExecutionDevice(deviceId, localFingerprint, devices),
    [deviceId, devices, localFingerprint],
  );
  const remoteDeviceID = executionDevice.pairedDeviceId;
  const remoteExecution = executionDevice.remote;
  // 只有存在已配对的 agentred 行时，本机才真能把供应商同步过去；本机自身指纹与
  // 账号内其它桌面端都没有这条通道，不提供做不到的同步入口。
  const canSyncProvider = remoteDeviceID > 0;
  // 有本机的宿主（桌面端）通过配对认识别的机器，选项就是那些配对行；没有本机的宿主
  // 只能通过账号认识它们，选项就是账号里的执行端设备。这两条路互斥，因为它们本来
  // 就是同一件事的两种知识来源。
  const deviceOptions = React.useMemo<DeviceOption[]>(
    () =>
      hasLocalDevice
        ? devices.map((device) => ({
            value: pairedDeviceSelectValue(device),
            name: device.name,
            online: device.online,
          }))
        : accountDeviceOptions(accountDevices),
    [accountDevices, devices, hasLocalDevice],
  );
  const accountDeviceNames = React.useMemo(
    () =>
      new Map(
        accountDevices
          .filter((device) => device.fingerprint)
          .map((device) => [device.fingerprint, device.name] as const),
      ),
    [accountDevices],
  );
  // 没有本机可指代时「本地」这一项根本不存在，于是空 deviceId 也没有对应的选项值。
  const localSelectValue = hasLocalDevice ? LOCAL_DEVICE_SELECT_VALUE : "";
  const selectedDeviceValue = deviceSelectValue(
    deviceId,
    localFingerprint,
    localSelectValue,
  );
  const selectedDeviceKnown =
    (hasLocalDevice && selectedDeviceValue === LOCAL_DEVICE_SELECT_VALUE) ||
    deviceOptions.some((option) => option.value === selectedDeviceValue);
  const deviceDisplayName = React.useCallback(
    (value: string) => {
      if (
        value === "" ||
        (localFingerprint !== "" && value === localFingerprint)
      ) {
        return hasLocalDevice
          ? t("agentBackends.device.localShort")
          : t("agentBackends.device.unspecified");
      }
      return (
        devices.find(
          (candidate) => pairedDeviceSelectValue(candidate) === value,
        )?.name ||
        accountDeviceNames.get(value) ||
        value
      );
    },
    [accountDeviceNames, devices, hasLocalDevice, localFingerprint, t],
  );

  return {
    deviceId,
    setDeviceId,
    devices,
    localFingerprint,
    accountDeviceNames,
    remoteDeviceID,
    remoteExecution,
    canSyncProvider,
    deviceOptions,
    localSelectValue,
    selectedDeviceValue,
    selectedDeviceKnown,
    deviceDisplayName,
  };
}
