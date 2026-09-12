// frontend/src/components/agentre/remote-devices/device-port-forward.tsx
//
// 设备行下的「端口转发」子块(规格 2026-09-06 决策 10):把共享包的
// `PortForwardSection` 接到 PortForward* 那五个 wails 绑定上。
//
// 这一层只做三件宿主专属的事:调绑定、把过桥的业务码翻成出路、拉系统浏览器
// (决策 11)。呈现规则(哪个控件在什么条件下渲染)全在共享包里,两个宿主共用。
import { useCallback, useEffect, useId, useRef, useState } from "react";
import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Button,
  Input,
  Label,
  PortForwardSection,
  copyTextWithToast,
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
 */
const DEVICE_OFFLINE = 21000;
const NOT_DECLARED = 21001;
const PORT_TAKEN = 21002;
const INVALID_PORT = 21003;

type Coded = { code: number; message: string };

/** 认不出码时 code 记 0 并把原文带上;前缀本身要剥掉,它是内部记号。 */
function classify(err: unknown): Coded {
  const raw = err instanceof Error ? err.message : String(err);
  const m = CODED_PREFIX.exec(raw);
  if (!m) return { code: 0, message: raw };
  return { code: Number(m[1]), message: m[2] };
}

// ── 组件 ─────────────────────────────────────────────────────────────────────

/** 按端口排序:排法归宿主(包内不排),同一台设备两次读到的顺序因此是稳定的。 */
function byPort(views: PortForwardMappingView[]): PortForwardMappingView[] {
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
  const fieldId = useId();
  const alive = useRef(true);

  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");
  const [loadError, setLoadError] = useState("");
  const [mappings, setMappings] = useState<PortForwardMappingView[]>([]);
  const [unreachable, setUnreachable] = useState(false);
  const [actionError, setActionError] = useState("");
  /**
   * 访问地址不由列举给出:桌面端那条 `127.0.0.1:<端口>` 要等 PortForwardOpen 绑上
   * 本机监听之后才存在。所以它单独记在这里,重取列表时不会被抹掉 —— 用户刚复制的
   * 那条地址此刻还是活的。
   */
  const [addresses, setAddresses] = useState<Record<string, string>>({});

  const [adding, setAdding] = useState(false);
  const [port, setPort] = useState("");
  const [name, setName] = useState("");
  const [addError, setAddError] = useState("");

  const deviceKey = String(deviceId);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const load = useCallback(async () => {
    try {
      const views = (await PortForwardList(
        deviceKey,
      )) as PortForwardMappingView[];
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

  const replaceRow = useCallback((view: PortForwardMappingView) => {
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
      const address = await PortForwardOpen(
        deviceKey,
        mapping.id,
        mapping.port,
      );
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
      )) as PortForwardMappingView;
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

  function openAddForm() {
    setAdding(true);
    setAddError("");
  }

  function closeAddForm() {
    setAdding(false);
    setPort("");
    setName("");
    setAddError("");
  }

  async function handleCreate(event: React.FormEvent) {
    event.preventDefault();
    const parsed = Number(port.trim());
    if (port.trim() === "" || !Number.isInteger(parsed)) {
      setAddError(t("remoteDevices.portForward.add.invalidPort"));
      return;
    }
    setAddError("");
    try {
      const view = (await PortForwardCreate(
        deviceKey,
        parsed,
        name.trim(),
      )) as PortForwardMappingView;
      if (!alive.current) return;
      setMappings((prev) => byPort([...prev, view]));
      closeAddForm();
    } catch (err) {
      if (!alive.current) return;
      const failure = classify(err);
      // 两个码都指向端口那一格,但出路不同:一个换端口,一个把号填对。
      if (failure.code === PORT_TAKEN) {
        setAddError(t("remoteDevices.portForward.add.portTaken"));
        return;
      }
      if (failure.code === INVALID_PORT) {
        setAddError(t("remoteDevices.portForward.add.invalidPort"));
        return;
      }
      if (failure.code === DEVICE_OFFLINE) {
        setUnreachable(true);
        closeAddForm();
        return;
      }
      setAddError(t("remoteDevices.portForward.add.failed", failure));
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
            ...m,
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
          onCreate={isOffline ? undefined : openAddForm}
          onToggleEnabled={handleToggle}
          onRemove={handleRemove}
        />
      ) : null}

      {actionError ? (
        <div className="text-xs text-destructive">{actionError}</div>
      ) : null}

      {adding && phase === "ready" && !isOffline ? (
        <form className="flex flex-col gap-1.5" onSubmit={handleCreate}>
          <div className="flex items-end gap-2">
            <div className="flex w-24 flex-col gap-1">
              <Label
                htmlFor={`${fieldId}-port`}
                className="text-2xs text-muted-foreground"
              >
                {t("remoteDevices.portForward.add.port")}
              </Label>
              <Input
                id={`${fieldId}-port`}
                type="number"
                inputMode="numeric"
                className="h-7 text-xs"
                aria-invalid={addError !== ""}
                value={port}
                onChange={(e) => setPort(e.target.value)}
              />
            </div>
            <div className="flex min-w-0 flex-1 flex-col gap-1">
              <Label
                htmlFor={`${fieldId}-name`}
                className="text-2xs text-muted-foreground"
              >
                {t("remoteDevices.portForward.add.name")}
              </Label>
              <Input
                id={`${fieldId}-name`}
                className="h-7 text-xs"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <Button type="submit" size="xs">
              {t("remoteDevices.portForward.add.submit")}
            </Button>
            <Button
              type="button"
              size="xs"
              variant="ghost"
              onClick={closeAddForm}
            >
              {t("remoteDevices.portForward.add.cancel")}
            </Button>
          </div>
          {addError ? (
            <div
              data-testid="port-forward-add-error"
              className="text-2xs text-destructive"
            >
              {addError}
            </div>
          ) : null}
        </form>
      ) : null}
    </div>
  );
}
