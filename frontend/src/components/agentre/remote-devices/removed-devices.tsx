import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@agentre-hub/agentre-ui";

import type { RemovedDeviceView } from "./use-remote-devices";

export type RemovedDevicesProps = {
  devices: RemovedDeviceView[];
  onRestore: (fingerprint: string) => Promise<void>;
};

// D15：用户从这台桌面移除过、却仍在账号里的机器不在设备列表里出现，只在这里逐台恢复。
// 默认收起：移除是用户自己的意图，这个入口只是给「请回来」留一条路，不该抢设备列表的位置。
export function RemovedDevices({ devices, onRestore }: RemovedDevicesProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState<string | null>(null);
  const [failed, setFailed] = useState<string | null>(null);

  if (devices.length === 0) return null;

  const restore = async (fingerprint: string) => {
    setPending(fingerprint);
    setFailed(null);
    try {
      await onRestore(fingerprint);
    } catch {
      // 失败原因是本机存储的事，原话对用户没有可操作的信息：只说没恢复成、可以再点。
      setFailed(fingerprint);
    } finally {
      setPending(null);
    }
  };

  const Chevron = open ? ChevronDown : ChevronRight;

  return (
    <div className="flex flex-col gap-2">
      <Button
        variant="ghost"
        size="sm"
        className="self-start text-muted-foreground"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <Chevron data-icon="inline-start" aria-hidden="true" />
        {t("remoteDevices.removed.toggle", { count: devices.length })}
      </Button>
      {open ? (
        <ul className="flex flex-col gap-2">
          {devices.map((d) => (
            <li
              key={d.fingerprint}
              className="flex items-center justify-between gap-3 rounded-lg border border-border px-4 py-2"
            >
              <span className="min-w-0 truncate text-sm">{d.name}</span>
              <div className="flex shrink-0 items-center gap-2">
                {failed === d.fingerprint ? (
                  <span className="text-xs text-status-error">
                    {t("remoteDevices.removed.restoreFailed")}
                  </span>
                ) : null}
                <Button
                  variant="outline"
                  size="sm"
                  disabled={pending === d.fingerprint}
                  onClick={() => void restore(d.fingerprint)}
                >
                  {t("remoteDevices.removed.restore")}
                </Button>
              </div>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
