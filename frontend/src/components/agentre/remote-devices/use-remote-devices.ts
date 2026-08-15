import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  RemoteDeviceList,
  RemoteDeviceAdd,
  RemoteDeviceRemove,
  RemoteDeviceUpdateTLS,
  RemoteDeviceRefresh,
  RemoteDeviceRename,
  ServerListDevices,
} from "../../../../wailsjs/go/app/App";
import { EventsOn } from "../../../../wailsjs/runtime/runtime";
import type {
  remote_device_svc,
  server_svc,
} from "../../../../wailsjs/go/models";

export type DeviceView = remote_device_svc.DeviceView;
export type AddRequest = remote_device_svc.AddRequest;

// ── R15 可达路径 ─────────────────────────────────────────────────────────────
// 一行一台机器:LAN 配对与账号归属两来源按设备指纹合并。该行呈现「可达路径」
// 而非凭据来源 —— 直连(LAN)与中转(账号)各自是一条路径。

export type PathKind = "lan" | "relay";
/** 路径状态:在用(高亮)/ 可用(存在但非当前在用)/ 失效(试过但不可达)。 */
export type PathState = "in-use" | "available" | "dead";

export type DevicePath = {
  kind: PathKind;
  state: PathState;
};

/** 账号来源。known=false 表示桌面未登录或没拉到清单 —— 此时无法判定「未认领」。 */
export type AccountSource = {
  known: boolean;
  devices: server_svc.Device[];
};

/** 一行设备里两种来源共有的部分。 */
type DeviceRowBase = {
  /**
   * 列表键。配对行 id 与账号设备 ID 是两个各自自增的序列,会撞号,
   * 所以键自带来源前缀。
   */
  key: string;
  name: string;
  /** 这台机器此刻有没有一条在用的可达路径。 */
  online: boolean;
  lastSeenAt: number;
  /** 可达路径 chips。 */
  paths: DevicePath[];
  /** 未认领:仅本机配对、账号清单里没有这台机器的指纹(且清单已知)。 */
  unclaimed: boolean;
  /** true → 主地址位显示「经中转」而非 LAN url(中转路径在用)。 */
  viaRelay: boolean;
};

/**
 * 有本机 LAN 配对行(paired_agentreds)的一行。TLS 信任、LAN 地址,以及刷新直连 /
 * 改名 / 改 TLS / 删配对这组动作,作用的都是 lan 这一行。
 */
export type LanDeviceRow = DeviceRowBase & {
  lan: DeviceView;
  /** 账号来源的设备(指纹匹配);undefined = 这台机器还没认领到账号。 */
  account?: server_svc.Device;
};

/** 账号独有的一行:账号清单里有、本机没有配对行 —— 中转是它唯一一条路径。 */
export type AccountDeviceRow = DeviceRowBase & {
  lan?: undefined;
  account: server_svc.Device;
};

/** 合并后的一行设备。lan 在不在,就是这一行有没有 LAN 来源。 */
export type DeviceRowModel = LanDeviceRow | AccountDeviceRow;

/**
 * 按设备指纹把两个来源合成「一行一台机器」:LAN 独有、两边都有、账号独有
 * 三种都产生行(全外连接)。以 LAN 为左表做左连接会漏掉最后一种 —— 一台
 * 只登记在账号里、本机从没配对过的 agentred 就整台看不见。
 */
export function mergeDeviceSources(
  lan: DeviceView[],
  account: AccountSource,
): DeviceRowModel[] {
  const accountByFp = new Map<string, server_svc.Device>();
  for (const d of account.devices) {
    if (d.Fingerprint && !accountByFp.has(d.Fingerprint)) {
      accountByFp.set(d.Fingerprint, d);
    }
  }
  const claimedFingerprints = new Set<string>();
  const rows: DeviceRowModel[] = lan.map((d) => {
    const acc = accountByFp.get(d.daemonFingerprint);
    if (acc) claimedFingerprints.add(d.daemonFingerprint);
    // url 为空 = 后端收编自账号的行(paired_agentred_entity.IsRelayOnly):本机有一行
    // 记录(它正是让这台机器能被选成「运行设备」的东西),但**没有** LAN 路径。画一条
    // 直连 chip 会把「本机从没配对过它」说成「直连在用」。
    const relayOnly = !d.url;
    const online = d.online;
    const paths: DevicePath[] = relayOnly
      ? []
      : [{ kind: "lan", state: online ? "in-use" : "dead" }];
    let relayInUse = false;
    if (relayOnly) {
      const relayReachable = acc?.Online ?? false;
      relayInUse = relayReachable;
      paths.push({ kind: "relay", state: relayReachable ? "in-use" : "dead" });
    } else if (acc) {
      // 中转路径是否可达,取 daemon 在 server 上的中继在线登记(R20),
      // 而不是账号侧的授权标志 —— R15 要求这一行呈现「可达路径」而非
      // 「凭据来源」。授权被撤销的机器无法再续期在线登记,Online 会自行落回 false。
      const relayReachable = acc.Online;
      relayInUse = relayReachable && !online;
      paths.push({
        kind: "relay",
        state: relayReachable ? (online ? "available" : "in-use") : "dead",
      });
    }
    return {
      key: `lan:${d.id}`,
      name: d.name,
      // 收编行没有 LAN 握手,d.online(由 last_seen_at 推出)对它没有意义:
      // 它是否可达完全取决于账号侧的中继登记。
      online: relayOnly ? relayInUse : online || relayInUse,
      lastSeenAt: d.lastSeenAt,
      lan: d,
      account: acc,
      paths,
      // 指纹为空的 LAN 行无从与账号清单对照(accountByFp 也不收空键),缺少依据时
      // 不下「未认领」的结论 —— 否则一台已认领的机器会被标成别人看不到它。
      unclaimed: account.known && !!d.daemonFingerprint && acc == null,
      viaRelay: relayInUse,
    };
  });

  // 账号独有:账号清单里有、没有任何配对行认领这个指纹。accountByFp 已按指纹
  // 去重、且不收空指纹 —— 没有指纹的账号行既无从与 LAN 对照,也无从经中转寻址。
  for (const acc of accountByFp.values()) {
    if (claimedFingerprints.has(acc.Fingerprint)) continue;
    // kind=desktop 的机器由 DesktopDeviceRow 单独成行(R19),这里再出一行
    // 就是同一台机器在面板上出现两次。
    if (acc.Kind === "desktop") continue;
    const relayReachable = acc.Online;
    rows.push({
      key: `account:${acc.ID}`,
      name: acc.Name || acc.Fingerprint,
      online: relayReachable,
      lastSeenAt: acc.LastSeenAt,
      account: acc,
      // 没有配对行 → 没有 LAN 路径,中转是唯一一条。
      paths: [{ kind: "relay", state: relayReachable ? "in-use" : "dead" }],
      // 它就在账号清单里,按定义不是未认领。
      unclaimed: false,
      viaRelay: relayReachable,
    });
  }

  return rows;
}

type StateEvent = {
  id: number;
  name: string;
  online: boolean;
  lastSeenAt: number;
  lastError: string;
};

const EVENT_NAME = "remote.device.state";

export type RemoteDevicesLoadState = "loading" | "error" | "ready";

export function useRemoteDevices() {
  const [lanDevices, setLanDevices] = useState<DeviceView[]>([]);
  const [account, setAccount] = useState<AccountSource>({
    known: false,
    devices: [],
  });
  const [loadState, setLoadState] = useState<RemoteDevicesLoadState>("loading");
  const hasLoaded = useRef(false);
  const reloadSequence = useRef(0);

  // 合并后的行 = LAN 来源 + 账号来源。在线态事件只改 LAN 来源,merge 重算路径。
  const devices = useMemo(
    () => mergeDeviceSources(lanDevices, account),
    [lanDevices, account],
  );

  const reload = useCallback(async () => {
    const sequence = ++reloadSequence.current;
    if (!hasLoaded.current) setLoadState("loading");

    try {
      const list = (await RemoteDeviceList()) ?? [];
      // 账号来源:未登录时 ServerListDevices 本地即返回 ErrNotLoggedIn(known=false,
      // 不判未认领);已登录但拉取失败(服务器离线)同样 known=false,不把每台都误标未认领。
      let acc: AccountSource;
      try {
        const accountList = (await ServerListDevices()) ?? [];
        acc = { known: true, devices: accountList };
      } catch {
        acc = { known: false, devices: [] };
      }
      if (sequence !== reloadSequence.current) return;
      setLanDevices(list);
      setAccount(acc);
      hasLoaded.current = true;
      setLoadState("ready");
    } catch (error) {
      if (sequence === reloadSequence.current && !hasLoaded.current) {
        setLoadState("error");
      }
      throw error;
    }
  }, []);

  useEffect(() => {
    void reload().catch(() => {});
  }, [reload]);

  useEffect(() => {
    const off = EventsOn(EVENT_NAME, (payload: unknown) => {
      const ev = payload as StateEvent;
      setLanDevices((prev) =>
        prev.map((d) =>
          d.id === ev.id
            ? {
                ...d,
                name: ev.name || d.name,
                online: ev.online,
                lastSeenAt: ev.lastSeenAt,
                lastError: ev.lastError,
              }
            : d,
        ),
      );
    });
    const onFocus = () => {
      void reload().catch(() => {});
    };
    window.addEventListener("focus", onFocus);
    return () => {
      off?.();
      window.removeEventListener("focus", onFocus);
    };
  }, [reload]);

  return {
    devices,
    accountDevices: account.devices,
    loadState,
    reload,
    add: async (req: AddRequest) => {
      await RemoteDeviceAdd(req);
      await reload();
    },
    remove: async (id: number) => {
      await RemoteDeviceRemove(id);
      await reload();
    },
    updateTLS: async (id: number, mode: string, pem: string) => {
      await RemoteDeviceUpdateTLS(id, mode, pem);
      await reload();
    },
    rename: async (id: number, name: string) => {
      await RemoteDeviceRename(id, name);
      await reload();
    },
    refresh: async (id: number) => {
      const v = await RemoteDeviceRefresh(id);
      setLanDevices((prev) => prev.map((x) => (x.id === id ? (v ?? x) : x)));
    },
  };
}
