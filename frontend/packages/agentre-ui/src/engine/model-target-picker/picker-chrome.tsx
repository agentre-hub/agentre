// 弹层的外围件：失效警示、远端设备说明、搜索框，以及底部常显说明条。
// 都只吃 props，没有一件持有列表状态。
import { useId, useState, type ReactNode, type RefObject } from "react";
import {
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  Loader2,
  Monitor,
  RefreshCw,
  Search,
  X,
} from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { Input } from "../../ui/input";
import { Button } from "../../ui/button";
import { CliModelRow } from "./option-row";
import {
  cliModelResolution,
  type CliModelChoice,
  type CliModelOption,
  type CliModelPickerState,
} from "./cli-models";

// CLI 目录是一段独立的登录账号选项；Claude / Codex / Pi 共用这一份交互。
export function CliModelSection({
  withinListbox = false,
  backendType,
  state,
  search,
  onSelect,
}: {
  backendType: string;
  state: CliModelPickerState;
  withinListbox?: boolean;
  search: string;
  onSelect: (choice: CliModelChoice) => void;
}) {
  const { t } = useTranslation();
  const cli = t(`agentBackends.backendType.${backendType}.shortLabel`);
  const [legacyOpen, setLegacyOpen] = useState(false);
  const [manual, setManual] = useState("");
  const legacyId = useId();
  const q = search.trim().toLowerCase();
  const matches = (model: CliModelOption) =>
    `${model.label} ${model.modelId} ${model.value} ${model.group ?? ""}`
      .toLowerCase()
      .includes(q);
  const models = state.models.filter(matches);
  const current = models.filter((model) => !model.legacy);
  const legacy = models.filter((model) => model.legacy);
  const choice = state.choice;
  const unknown =
    choice?.mode === "fixed" &&
    !state.models.some((model) => model.value === choice.value)
      ? choice.value
      : "";
  const resolution = cliModelResolution(state, { mode: "cli-default" });
  const renderModels = (items: CliModelOption[]) => {
    const groups = new Map<string, CliModelOption[]>();
    for (const model of items) {
      const group = model.group ?? "";
      groups.set(group, [...(groups.get(group) ?? []), model]);
    }
    return Array.from(groups, ([group, options]) => (
      <div key={group} role="group" aria-label={group || undefined}>
        {group ? (
          <div className="px-2 pb-1 pt-2 text-2xs text-muted-foreground">
            {group}
          </div>
        ) : null}
        {options.map((model) => (
          <CliModelRow
            key={model.value}
            label={model.label || model.value}
            sublabel={model.modelId}
            selected={choice?.mode === "fixed" && choice.value === model.value}
            onSelect={() => onSelect({ mode: "fixed", value: model.value })}
          />
        ))}
      </div>
    ));
  };
  const submitManual = () => {
    const value = manual.trim();
    if (value) onSelect({ mode: "fixed", value });
  };
  return (
    <section
      data-cli-model-section
      className="mb-2"
      onKeyDown={(event) => {
        // 手输框保留文本编辑方向键，选项行则交给整段 Picker 导航。
        if (event.target instanceof HTMLInputElement && event.key !== "Escape")
          event.stopPropagation();
      }}
    >
      <div className="flex items-center gap-1 px-2 py-1.5 text-2xs font-semibold text-muted-foreground">
        <span className="flex-1">
          {t("modelTargetPicker.cli.heading", { cli })}
        </span>
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="size-6"
          disabled={state.loading}
          aria-label={t("modelTargetPicker.cli.refresh")}
          onClick={state.onRefresh}
        >
          <RefreshCw className="size-3" aria-hidden="true" />
        </Button>
      </div>
      {state.loading ? (
        <div
          role="status"
          className="flex items-center gap-2 px-2 py-2 text-xs text-muted-foreground"
        >
          <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
          {t("modelTargetPicker.cli.loading", { cli })}
        </div>
      ) : null}
      {state.error ? (
        <div
          role="alert"
          className="mx-1 my-1 rounded-lg bg-status-waiting-bg p-2 text-2xs text-status-waiting-text"
        >
          {t("modelTargetPicker.cli.error", { cli, error: state.error })}
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={state.loading}
            onClick={state.onRefresh}
          >
            {t("modelTargetPicker.cli.retry")}
          </Button>
        </div>
      ) : null}
      <div
        role={withinListbox ? "group" : "listbox"}
        aria-label={t("modelTargetPicker.cli.heading", { cli })}
      >
        <CliModelRow
          label={t("modelTargetPicker.cli.followDefault")}
          follow
          sublabel={
            resolution.modelId
              ? `${resolution.label} · ${resolution.modelId}`
              : t("modelTargetPicker.cli.decides")
          }
          selected={choice?.mode === "cli-default"}
          onSelect={() => onSelect({ mode: "cli-default" })}
        />
        {renderModels(current)}
        {legacy.length ? (
          <>
            <Button
              type="button"
              variant="ghost"
              className="h-7 w-full justify-start text-2xs"
              aria-expanded={legacyOpen || !!q}
              aria-controls={legacyId}
              onClick={() => setLegacyOpen((value) => !value)}
            >
              {legacyOpen || q ? (
                <ChevronDown className="size-3" aria-hidden="true" />
              ) : (
                <ChevronRight className="size-3" aria-hidden="true" />
              )}
              {t("modelTargetPicker.cli.otherVersions", {
                count: legacy.length,
              })}
            </Button>
            <div id={legacyId} hidden={!legacyOpen && !q}>
              {legacyOpen || q ? renderModels(legacy) : null}
            </div>
          </>
        ) : null}
        {unknown && (!q || unknown.toLowerCase().includes(q)) ? (
          <CliModelRow
            label={unknown}
            sublabel={t("modelTargetPicker.cli.manualSaved")}
            selected
            onSelect={() => onSelect({ mode: "fixed", value: unknown })}
          />
        ) : null}
      </div>
      <div className="flex gap-1 p-2">
        <Input
          className="h-8 text-xs"
          value={manual}
          aria-label={t("modelTargetPicker.cli.manualLabel")}
          placeholder={t("modelTargetPicker.cli.manualPlaceholder")}
          onChange={(event) => setManual(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              submitManual();
            }
          }}
        />
        <Button
          type="button"
          size="sm"
          variant="ghost"
          disabled={!manual.trim()}
          onClick={submitManual}
        >
          {t("modelTargetPicker.cli.useManual")}
        </Button>
      </div>
    </section>
  );
}

/** 失效目标顶部警示（弹层内上方，不是底部 footer）。 */
export function InvalidTargetBanner({ targetLabel }: { targetLabel: string }) {
  const { t } = useTranslation();
  return (
    // mockup 把警示做成 .psearch 里的一张圆角 .banner.warn，不是通栏横条。
    <div className="p-2">
      <div
        data-testid="invalid-banner"
        className="flex items-start gap-2 rounded-lg bg-status-waiting-bg px-2.5 py-2 text-2xs text-status-waiting-text"
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
      className="flex items-center gap-1.5 px-3 py-2 text-2xs text-muted-foreground"
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
    <div className="p-2">
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
          className="h-8 rounded-[7px] pl-7 pr-7 text-xs"
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
    <div className="bg-card px-2.5 py-[7px] text-2xs text-muted-foreground">
      {children}
    </div>
  );
}
