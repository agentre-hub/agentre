// 弹层的外围件：失效警示、远端设备说明、搜索框，以及底部常显说明条。
// 都只吃 props，没有一件持有列表状态。
import type { ReactNode, RefObject } from "react";
import { AlertTriangle, Monitor, Search, X } from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { Input } from "../../ui/input";

/** 失效目标顶部警示（弹层内上方，不是底部 footer）。 */
export function InvalidTargetBanner({ targetLabel }: { targetLabel: string }) {
  const { t } = useTranslation();
  return (
    // mockup 把警示做成 .psearch 里的一张圆角 .banner.warn，不是通栏横条。
    <div className="border-b border-border p-2">
      <div
        data-testid="invalid-banner"
        className="flex items-start gap-2 rounded-lg bg-status-waiting-bg px-2.5 py-2 text-2xs text-status-waiting"
      >
        <AlertTriangle className="mt-px size-3.5 shrink-0" aria-hidden="true" />
        <span>
          <strong className="font-semibold">
            {t("modelTargetPicker.invalidTitle")}
          </strong>
          <br />
          {t("modelTargetPicker.invalidTarget", { target: targetLabel })}
        </span>
      </div>
    </div>
  );
}

/** 远端场景：说明以目标设备的配置为准（未传设备名 = 本机，调用方不渲染）。 */
export function RemoteDeviceHeader({ deviceLabel }: { deviceLabel: string }) {
  const { t } = useTranslation();
  return (
    <div
      data-testid="remote-device-header"
      className="flex items-center gap-1.5 border-b border-border px-3 py-2 text-2xs text-muted-foreground"
    >
      <Monitor className="size-3.5 shrink-0" aria-hidden="true" />
      <span className="truncate">
        {t("modelTargetPicker.remoteDeviceHeader", { device: deviceLabel })}
      </span>
    </div>
  );
}

/** 搜索框（mockup .psearch > .search）：有边框的输入框，放大镜绝对定位在框内左侧。 */
export function PickerSearchBox({
  value,
  onChange,
  onClear,
  inputRef,
}: {
  value: string;
  onChange: (value: string) => void;
  // 清空按钮与敲键不是同一个动作：敲键会把活动项拉回首项，清空不动它。
  onClear: () => void;
  inputRef: RefObject<HTMLInputElement | null>;
}) {
  const { t } = useTranslation();
  return (
    <div className="border-b border-border p-2">
      <div className="relative">
        <Search
          className="pointer-events-none absolute left-[9px] top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          ref={inputRef}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={t("modelTargetPicker.searchPlaceholder")}
          className="h-8 rounded-[7px] bg-input-bg pl-7 pr-7 text-xs dark:bg-input-bg"
        />
        {value ? (
          <button
            type="button"
            aria-label={t("modelTargetPicker.clearSearch")}
            className="absolute right-1.5 top-1/2 shrink-0 -translate-y-1/2 cursor-pointer rounded p-0.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            onClick={() => {
              onClear();
              inputRef.current?.focus();
            }}
          >
            <X className="size-3.5" aria-hidden="true" />
          </button>
        ) : null}
      </div>
    </div>
  );
}

/** 底部常显说明条（同步后果 / 远端缺失 / 消费方自带 footer 共用同一条外壳）。 */
export function PickerNote({ children }: { children: ReactNode }) {
  return (
    <div className="border-t border-border bg-card px-2.5 py-[7px] text-2xs text-muted-foreground">
      {children}
    </div>
  );
}
