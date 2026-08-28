// 共享 ModelTargetPicker（spec「UI, accessibility and recent targets」）。
//
// 三处复用同一主体，只替换顶部特殊项（scenario）：
//   - backend：顶部特殊项 = CLI 自身登录态（native）；
//   - chat：顶部特殊项 = 跟随 Agent 绑定（inherit-agent）；
//   - route：顶部特殊项 = 继承主绑定（inherit-main）。
//
// 主体交互：搜索、最近使用（localStorage，按执行位置指纹隔离，单行横向可移除 chip）、
// Provider 分组（sticky 组头承载品牌标识 + 供应商名）、provider-default 首项（强调底色 +
// 动态图标 + 当前解析模型）、fixed-model 列表（display name + 上下文/最大输出）、
// 兼容性过滤（effective backend type）、loading/empty/error/invalid/remote 状态、
// 键盘导航（方向键 / Enter / Esc / focus ring）。
// 只通过 onChange 发射 providerKey/modelKey，绝不发射名称 / ModelID / 凭据。
//
// 本文件只做装配：派生与交互在 ./use-picker-options，选项怎么算在 ./options，
// 一行怎么长在 ./option-row，列表怎么排在 ./picker-option-list，触发按钮在
// ./picker-trigger，最近使用横条在 ./recent-chips，弹层外围件在 ./picker-chrome。
import * as React from "react";
import { useUiTranslation as useTranslation } from "../../i18n";
import { Loader2 } from "lucide-react";

import { Popover, PopoverContent, PopoverTrigger } from "../../ui/popover";

import { resolveTargetLabel } from "./options";
import {
  InvalidTargetBanner,
  PickerNote,
  PickerSearchBox,
  RemoteDeviceHeader,
} from "./picker-chrome";
import { PickerOptionList } from "./picker-option-list";
import { PickerTrigger } from "./picker-trigger";
import { RecentChipsRow } from "./recent-chips";
import { usePickerOptions } from "./use-picker-options";
import {
  isNativeTarget,
  type ModelTarget,
  type PickerProvider,
  type PickerScenario,
} from "./types";

export type ModelTargetPickerProps = {
  scenario: PickerScenario;
  // backendType 是 effective backend 类型，用于 provider.type 兼容性过滤。
  backendType: string;
  // executionLocation 空串 = 本机；非空 = 远端设备 id（最近使用指纹隔离）。
  executionLocation?: string;
  selected: ModelTarget | null;
  onChange: (target: ModelTarget) => void;
  catalog: PickerProvider[];
  loading?: boolean;
  // error：模型目录拉取失败 → 弹层错误行。errorText 覆盖默认错误文案（chat 场景把
  // 持久化切换失败的真实信息透出来）。
  error?: boolean;
  errorText?: string;
  disabled?: boolean;
  // openOnMount：直达更换绑定等入口打开消费方时，同时展开选择器。
  openOnMount?: boolean;
  // invalid：当前选中的 target 在目录里解析不出来（Provider/Model 缺失/停用/被删）。
  invalid?: boolean;
  // remoteMissing：目标执行设备上缺少所选 Provider（远端场景提示，task 6 深化）。
  remoteMissing?: boolean;
  // supportsFixedModel：目标执行设备是否公布 llm-model-target-v1 能力位（task 6 决策 11）。
  // 远端且为 false 时禁用所有 fixed-model 选项，避免旧 daemon 静默降级为默认模型；
  // 本机 / 未传 → 默认 true（不限制）。
  supportsFixedModel?: boolean;
  // remoteCatalog：目标执行设备的 daemon 目录（task 6 决策 12，远端可运行事实源）。
  // 未传 = 本机（不启用远端门控）。传了以后，desktop 目录里在 daemon 上不存在的
  // Provider/Model 被禁用并标注「需同步」，绝不保存一个未经验证的远端目标。
  remoteCatalog?: PickerProvider[];
  // deviceLabel：目标执行设备的人读名字。传了以后弹层顶部说明「在该设备上执行、以该设备
  // 的配置为准」，并给设备上已有的供应商组打标记；未传（本机 / 消费方还没解析出名字）
  // 时整块不渲染。
  deviceLabel?: string;
  // specialSublabel：顶部特殊项的解析结果副行（chat/route 由消费方解析后传入，
  // 写出「供应商 · 模型」或「跟随该 Agent 绑定的供应商」）。backend 场景未传时
  // 回落「由 CLI 自身的登录账号决定」。可以是节点（带品牌标识的「→ [logo] 供应商 ·
  // 模型」）；节点只参与渲染，特殊项的搜索始终只按 specialLabel 匹配（见 Option.searchText）。
  specialSublabel?: React.ReactNode;
  // 远端目录缺少本机 Provider 时的显式同步入口；消费方负责确认与执行凭证复制。
  onSyncProvider?: (provider: PickerProvider) => void;
  // footer：弹层底部常显说明（chat 场景的「自下一轮生效」等），随弹层一起出现。
  footer?: React.ReactNode;
  // compact：表单内嵌（claude tier 路由）用小号触发按钮。
  compact?: boolean;
  align?: "start" | "end";
  className?: string;
  title?: string;
  // triggerLabel：覆盖触发按钮主行。chat 场景「未选但 agent 已绑 provider」时显示
  // 绑定供应商名，而不是顶部特殊项「跟随 Agent 绑定」；undefined 时按目录解析。
  // 可以是节点（backend 编辑器的主行 = 品牌标识 + 供应商名 + 跟随/固定徽标）；此时
  // 按钮的无障碍名改由 aria-label 决定，节点内容不参与名字计算（见 triggerAriaLabel）。
  triggerLabel?: React.ReactNode;
  // triggerSub：可选触发按钮副行。后端编辑器用它展示当前解析出的生效模型与模式后果；
  // 未传时保持 model-pill / chat 等既有消费方的单行形态。
  triggerSub?: React.ReactNode;
  "data-testid"?: string;
  "aria-label"?: string;
};

export function ModelTargetPicker({
  scenario,
  backendType,
  executionLocation = "",
  selected,
  onChange,
  catalog,
  loading = false,
  error = false,
  errorText,
  disabled = false,
  openOnMount = false,
  invalid = false,
  remoteMissing = false,
  supportsFixedModel = true,
  remoteCatalog,
  deviceLabel,
  specialSublabel,
  onSyncProvider,
  footer,
  compact = false,
  align = "start",
  className,
  title,
  triggerLabel,
  triggerSub,
  "data-testid": testId,
  "aria-label": ariaLabel,
}: ModelTargetPickerProps) {
  const { t } = useTranslation();
  const picker = usePickerOptions({
    scenario,
    backendType,
    executionLocation,
    selected,
    onChange,
    catalog,
    invalid,
    supportsFixedModel,
    remoteCatalog,
    specialSublabel,
    onSyncProvider,
    openOnMount,
    t,
  });
  const { compatible, flatOptions, remoteByKey, remoteGated } = picker;

  const triggerText = triggerLabel ?? picker.selectedLabel;

  const recentChipsRow = picker.showChips ? (
    <RecentChipsRow
      chips={picker.recents}
      onPick={picker.handlePick}
      onRemove={picker.handleRemoveRecent}
    />
  ) : null;

  return (
    <Popover open={picker.open} onOpenChange={picker.handleOpenChange}>
      <PopoverTrigger asChild>
        <PickerTrigger
          open={picker.open}
          disabled={disabled}
          invalid={invalid}
          compact={compact}
          className={className}
          title={title}
          testId={testId}
          ariaLabel={ariaLabel}
          selectedLabel={picker.selectedLabel}
          triggerText={triggerText}
          triggerSub={triggerSub}
        />
      </PopoverTrigger>
      <PopoverContent
        align={align}
        side="bottom"
        sideOffset={6}
        data-testid="model-target-popover"
        // 348px / 10px 圆角 = mockup .pop（index.html data-view="picker"）；overflow-hidden
        // 让搜索区与 foot 的分隔线贴着圆角收口。min(...) 在极窄视口兜底收缩，
        // 不会把弹层推出屏幕（860×640 最小窗口下恒等于 348px）。
        className="w-[min(348px,calc(100vw-2rem))] overflow-hidden rounded-[10px] p-0"
        onKeyDown={picker.handleKeyDown}
      >
        {invalid ? (
          <InvalidTargetBanner
            targetLabel={selected ? resolveTargetLabel(selected, catalog) : ""}
          />
        ) : null}

        {deviceLabel ? <RemoteDeviceHeader deviceLabel={deviceLabel} /> : null}

        <PickerSearchBox
          value={picker.search}
          onChange={(value) => {
            picker.setSearch(value);
            picker.setActiveIndex(0);
          }}
          onClear={() => picker.setSearch("")}
          inputRef={picker.searchRef}
        />

        {/* 330px / 5px = mockup .pop .plistw。 */}
        <div className="max-h-[330px] overflow-y-auto p-[5px]">
          {loading ? (
            <div className="flex items-center gap-2 px-2.5 py-3 text-xs text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
              {t("modelTargetPicker.loading")}
            </div>
          ) : error ? (
            <div className="px-2.5 py-3 text-xs text-status-waiting">
              {errorText ?? t("modelTargetPicker.error")}
            </div>
          ) : catalog.length === 0 && isNativeTarget(selected) ? (
            <div className="px-2.5 py-3 text-xs text-muted-foreground">
              {t("modelTargetPicker.empty")}
            </div>
          ) : catalog.length > 0 && compatible.length === 0 ? (
            <div className="px-2.5 py-3 text-xs text-muted-foreground">
              {t("modelTargetPicker.noCompatibleProviders")}
            </div>
          ) : null}

          {!loading && !error && flatOptions.length > 0 ? (
            <PickerOptionList
              listRef={picker.listRef}
              ariaLabel={ariaLabel}
              options={flatOptions}
              activeIndex={picker.activeIndex}
              selected={selected}
              scenario={scenario}
              SpecialIcon={picker.SpecialIcon}
              syncAnchorByProvider={picker.syncAnchorByProvider}
              compatible={compatible}
              // `remoteGated` 为真即蕴含 remoteByKey 非空；写成可选链只是因为
              // 别名收窄跨不进这个闭包。
              isProviderOnDevice={(providerKey) =>
                remoteGated && !!remoteByKey?.has(providerKey)
              }
              onSyncProvider={onSyncProvider}
              onActivate={picker.setActiveIndex}
              onSelect={picker.handlePick}
              recentChipsRow={recentChipsRow}
            />
          ) : null}
        </div>

        {/* 灰色项的原因每一行自己已经写了，底部不再总述一遍；这里只承诺同步的后果
            （mockup 远端弹层的 foot 也只有这一句）。 */}
        {remoteGated &&
        onSyncProvider &&
        picker.catalogOptions.some((o) => o.disabledHint) ? (
          <PickerNote>{t("modelTargetPicker.syncFootnote")}</PickerNote>
        ) : null}
        {remoteMissing ? (
          <PickerNote>{t("modelTargetPicker.remoteMissing")}</PickerNote>
        ) : null}
        {footer ? <PickerNote>{footer}</PickerNote> : null}
      </PopoverContent>
    </Popover>
  );
}

export {
  readRecentTargets,
  recordRecentTarget,
  removeRecentTarget,
} from "./recents";
export type {
  ModelTarget,
  PickerModel,
  PickerProvider,
  PickerScenario,
} from "./types";
export { buildPickerCatalog, useModelTargetCatalog } from "./catalog";
export {
  isNativeTarget,
  providerCompatibleForBackend,
  sameTarget,
} from "./types";
export {
  ProviderPillResolution,
  ProviderPillTrigger,
} from "./provider-pill-trigger";
export type { ProviderPillState } from "./provider-pill-trigger";
export { resolveProviderPillState } from "./pill-state";
export type { ProviderPillStateInput } from "./pill-state";
