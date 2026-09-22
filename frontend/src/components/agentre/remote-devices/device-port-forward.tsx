// frontend/src/components/agentre/remote-devices/device-port-forward.tsx
//
// 设备行下的「端口转发」子块(规格 2026-09-06 决策 10,2026-09-21 改收目标写法):
// 把共享包的 `PortForwardSection` 接到 PortForward* 那五个 wails 绑定上。
//
// 这一层只做三件宿主专属的事:调绑定、把过桥的业务码翻成出路、拉系统浏览器
// (决策 11)。呈现规则与新增表单(哪个控件在什么条件下渲染、目标写法怎么校验)
// 全在共享包里,两个宿主共用。
import { useCallback, useEffect, useRef, useState } from "react";
import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  PortForwardSection,
  copyTextWithToast,
  useUiTranslation,
  type PortForwardCreateInput,
  type PortForwardMappingView,
} from "@agentre-hub/agentre-ui";

import {
  PortForwardCreate,
  PortForwardDelete,
  PortForwardList,
  PortForwardOpen,
  PortForwardSetEnabled,
} from "../../../../wailsjs/go/app/App";
import { BrowserOpenURL } from "../../../../wailsjs/runtime/runtime";

// ── 过桥的业务码 ─────────────────────────────────────────────────────────────

/** `internal/app/coded_error.go` 那一侧的同一条契约。 */
const CODED_PREFIX = /^agentre-code:(\d+)\s?([\s\S]*)$/;

/**
 * `internal/pkg/code/code.go` 的 21000~ 段(稳定 wire 值)。按码分辨,不按文案 ——
 * 文案一改就静默失灵,而且中英两套要各猜一遍。
 *
 * `INVALID_PORT`(21003)不再从新增声明这条路上回来了:新增改收目标写法之后,
 * 端口越界这条理由并进了 `INVALID_TARGET`(21004,对应设备侧 -32076,见规格
 * 「映射与目标」)。
 */
const DEVICE_OFFLINE = 21000;
const NOT_DECLARED = 21001;
const PORT_TAKEN = 21002;
const INVALID_TARGET = 21004;

type Coded = { code: number; message: string };

/** 认不出码时 code 记 0 并把原文带上;前缀本身要剥掉,它是内部记号。 */
function classify(err: unknown): Coded {
  const raw = err instanceof Error ? err.message : String(err);
  const m = CODED_PREFIX.exec(raw);
  if (!m) return { code: 0, message: raw };
  return { code: Number(m[1]), message: m[2] };
}

// ── 组件 ─────────────────────────────────────────────────────────────────────

/**
 * wails 绑定交回的原始形状:比共享包的 `PortForwardMappingView` 多一个 `port`
 * (Go 侧仍然算出来,专给这一层排序用——共享包只认规范化后的 `target`,排法归
 * 宿主,包内不排)。
 */
type DeviceMapping = {
  id: string;
  port: number;
  name: string;
  enabled: boolean;
  target: string;
  address?: string;
};

/** 按端口排序:排法归宿主(包内不排),同一台设备两次读到的顺序因此是稳定的。 */
function byPort(views: DeviceMapping[]): DeviceMapping[] {
  return [...views].sort((a, b) => a.port - b.port);
}

type Props = {
  /** paired_agentred.id;绑定那一侧收的是字符串(int64 过桥到 number 会掉精度)。 */
  deviceId: number;
  /** 设备行已经知道的在线判定;列举失败拿到 21000 时同样会转成离线。 */
  offline: boolean;
  /** 离线句后面那句补充(相对时间由设备行格式化)。 */
  offlineDetail?: string;
};

export function DevicePortForward({ deviceId, offline, offlineDetail }: Props) {
  const { t } = useTranslation();
  // 「无效目标」这一句不在这个宿主的语言包里:规格「映射与目标」决策 15 把它定成
  // 所有宿主同一句,归共享包 `agentre-ui` 持有(`portForward.add.invalidTarget`),
  // 表单即时校验已经在用它,这里只是设备 -32076 回绝这条路也接到同一个出处。
  const { t: uiT } = useUiTranslation();
  const alive = useRef(true);

  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");
  const [loadError, setLoadError] = useState("");
  const [mappings, setMappings] = useState<DeviceMapping[]>([]);
  const [unreachable, setUnreachable] = useState(false);
  const [actionError, setActionError] = useState("");
  /**
   * 访问地址不由列举给出:桌面端那条 `127.0.0.1:<端口>` 要等 PortForwardOpen 绑上
   * 本机监听之后才存在。所以它单独记在这里,重取列表时不会被抹掉 —— 用户刚复制的
   * 那条地址此刻还是活的。
   */
  const [addresses, setAddresses] = useState<Record<string, string>>({});

  const deviceKey = String(deviceId);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const load = useCallback(async () => {
    try {
      const views = (await PortForwardList(deviceKey)) as DeviceMapping[];
      if (!alive.current) return;
      setMappings(byPort(views ?? []));
      setUnreachable(false);
      setPhase("ready");
    } catch (err) {
      if (!alive.current) return;
      const failure = classify(err);
      // 够不着那台机器时不是「读失败」,而是离线态:已列出的行留着,新增入口收起来。
      if (failure.code === DEVICE_OFFLINE) {
        setUnreachable(true);
        setPhase("ready");
        return;
      }
      setLoadError(failure.message);
      setPhase("error");
    }
  }, [deviceKey]);

  useEffect(() => {
    setPhase("loading");
    void load();
  }, [load]);

  /**
   * 设备回来了就重取一次。
   *
   * 离线态必须是可逆的:unreachable 只在一次**成功的列举**里被清掉,而列举只在挂载
   * 时跑过一次(这个小块折叠展开才重挂)。少了这一条,首屏就离线的那一次会把它永久
   * 钉在离线态上 —— 设备早回来了,它还说着「够不着」、新增入口一直收着,而这一页上
   * 没有任何让用户重试的入口。只认真→假这一次跳变:挂载时那一次由上面那条 effect
   * 负责,重复跑只是白白多一次往返。
   */
  const wasOffline = useRef(offline);
  useEffect(() => {
    const recovered = wasOffline.current && !offline;
    wasOffline.current = offline;
    if (recovered) void load();
  }, [offline, load]);

  /**
   * 一次动作真的到了那台设备。此前记下的「够不着」就此作废 —— 否则一次偶发的离线会
   * 把离线句和收起来的新增入口一直留在那里,而旁边的开关明明按得动。
   */
  const markReached = useCallback(() => setUnreachable(false), []);

  /** 三种「不是这一次动作本身的错」在这里统一收口。 */
  const handleFailure = useCallback(
    (err: unknown) => {
      const failure = classify(err);
      if (failure.code === DEVICE_OFFLINE) {
        setUnreachable(true);
        return;
      }
      // 那条声明在设备上已经没了(别的客户端刚删掉):手上这份列表是旧的,重取。
      if (failure.code === NOT_DECLARED) {
        void load();
        return;
      }
      setActionError(t("remoteDevices.portForward.actionFailed", failure));
    },
    [load, t],
  );

  const replaceRow = useCallback((view: DeviceMapping) => {
    setMappings((prev) =>
      byPort(prev.map((m) => (m.id === view.id ? view : m))),
    );
  }, []);

  const dropAddress = useCallback((id: string) => {
    setAddresses((prev) => {
      if (!(id in prev)) return prev;
      const next = { ...prev };
      delete next[id];
      return next;
    });
  }, []);

  async function handleOpen(mapping: PortForwardMappingView) {
    setActionError("");
    try {
      const address = await PortForwardOpen(deviceKey, mapping.id);
      if (!alive.current) return;
      markReached();
      setAddresses((prev) => ({ ...prev, [mapping.id]: address }));
      // 打开成功只代表监听绑上了;能不能访问得通由设备侧判,拒绝落在浏览器那一页。
      BrowserOpenURL(address);
    } catch (err) {
      if (!alive.current) return;
      handleFailure(err);
    }
  }

  async function handleToggle(
    mapping: PortForwardMappingView,
    enabled: boolean,
  ) {
    setActionError("");
    try {
      const view = (await PortForwardSetEnabled(
        deviceKey,
        mapping.id,
        enabled,
      )) as DeviceMapping;
      if (!alive.current) return;
      markReached();
      replaceRow(view);
      // 设备确认停用之后那条本机监听自己关了,手上这条地址随之失效。
      if (!enabled) dropAddress(mapping.id);
    } catch (err) {
      if (!alive.current) return;
      handleFailure(err);
    }
  }

  async function handleRemove(mapping: PortForwardMappingView) {
    setActionError("");
    try {
      await PortForwardDelete(deviceKey, mapping.id);
      if (!alive.current) return;
      markReached();
      setMappings((prev) => prev.filter((m) => m.id !== mapping.id));
      dropAddress(mapping.id);
    } catch (err) {
      if (!alive.current) return;
      handleFailure(err);
    }
  }

  function handleCopy(mapping: PortForwardMappingView) {
    const address = addresses[mapping.id];
    if (!address) return;
    void copyTextWithToast(address, {
      successTitle: t("remoteDevices.portForward.copyDone"),
      errorTitle: t("remoteDevices.portForward.copyFailed"),
    });
  }

  /**
   * 新增表单本身(哪个字段、https 才出的勾选框、客户端即时校验)全在共享包里,
   * 这里只管把提交的载荷送到设备、把设备回来的业务码翻成一句人话。resolve 让
   * 表单关掉、reject 把 `error.message` 显示在表单里 —— 度够不着设备这一种例外:
   * 静默转离线态,不当错误显示(表单已经在共享包里因为 `offline` 变真而自己关掉)。
   */
  async function handleCreate(input: PortForwardCreateInput) {
    try {
      const view = (await PortForwardCreate(
        deviceKey,
        input.target,
        input.name,
        input.insecure,
      )) as DeviceMapping;
      if (!alive.current) return;
      markReached();
      setMappings((prev) => byPort([...prev, view]));
    } catch (err) {
      if (!alive.current) return;
      const failure = classify(err);
      if (failure.code === DEVICE_OFFLINE) {
        setUnreachable(true);
        return;
      }
      // 判定权威恒在设备侧(规格「映射与目标」决策 8):两个码都指着目标那一格,
      // 但出路不同——一个换个目标,一个把写法改对(3000 / host:port /
      // http(s)://host[:port] 三种之外的,以及路径/查询串/用户信息/端口越界/
      // 主机为空,六种理由都归 -32076)。
      if (failure.code === PORT_TAKEN) {
        throw new Error(t("remoteDevices.portForward.add.targetTaken"), {
          cause: err,
        });
      }
      if (failure.code === INVALID_TARGET) {
        throw new Error(uiT("portForward.add.invalidTarget"), {
          cause: err,
        });
      }
      throw new Error(t("remoteDevices.portForward.add.failed", failure), {
        cause: err,
      });
    }
  }

  const isOffline = offline || unreachable;

  return (
    <div
      data-testid="device-port-forward"
      className="flex flex-col gap-1.5 rounded-md border border-border bg-secondary/40 px-3 py-2"
    >
      {phase === "loading" ? (
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />
          {t("remoteDevices.portForward.loading")}
        </div>
      ) : null}

      {phase === "error" ? (
        <div className="text-xs text-destructive">
          {t("remoteDevices.portForward.loadFailed", { message: loadError })}
        </div>
      ) : null}

      {phase === "ready" ? (
        <PortForwardSection
          mappings={mappings.map((m) => ({
            id: m.id,
            name: m.name,
            enabled: m.enabled,
            target: m.target,
            address: addresses[m.id],
          }))}
          offline={isOffline}
          offlineDetail={offlineDetail}
          openLabel={t("remoteDevices.portForward.open")}
          onOpen={handleOpen}
          onCopyAddress={handleCopy}
          // 离线时收起来的只有**新增入口**:它领向一张填完必然提交失败的表单,是条
          // 多步的死路。已经列出来的那些行整行保留、只是打不开(规格「三种非常态」)
          // —— 启停与删除照常画,按下去撞上离线由 handleFailure 收成离线态,不是
          // 一句机器原话。
          onCreate={isOffline ? undefined : handleCreate}
          onToggleEnabled={handleToggle}
          onRemove={handleRemove}
        />
      ) : null}

      {actionError ? (
        <div className="text-xs text-destructive">{actionError}</div>
      ) : null}
    </div>
  );
}
