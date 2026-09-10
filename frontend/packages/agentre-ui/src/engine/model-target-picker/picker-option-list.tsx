/**
 * 弹层里那个 listbox：把扁平化后的候选排成「组头 / 固定模型小节 / 行」。
 *
 * 分组不是在数据里预先切好的，而是**看相邻两行是否同组**当场决定要不要画组头——
 * 搜索会把中间的行滤掉，预切的分组在过滤后会留下空标题。
 *
 * 「最近使用」那一条横排紧跟在顶部特殊项之后，由调用方把整块传进来：它读的是
 * localStorage，与这份纯呈现件不是一件事。
 */
import * as React from "react";

import { useUiTranslation as useTranslation } from "../../i18n";

import { PickerOptionRow } from "./option-row";
import type { Option } from "./options";
import type { ModelTarget, PickerProvider, PickerScenario } from "./types";
import { sameTarget } from "./types";
import type { LucideIcon } from "lucide-react";

export interface PickerOptionListProps {
  listRef: React.RefObject<HTMLUListElement | null>;
  ariaLabel?: string;
  options: Option[];
  activeIndex: number;
  selected: ModelTarget | null;
  scenario: PickerScenario;
  SpecialIcon: LucideIcon;
  /** 该行是不是所属供应商第一条待同步的行（只在那一行挂同步入口）。 */
  syncAnchorByProvider: Map<string, string>;
  compatible: PickerProvider[];
  /** 远端场景下该供应商在目标设备上是否已存在；本机恒 false。 */
  isProviderOnDevice: (providerKey: string) => boolean;
  onSyncProvider?: (provider: PickerProvider) => void;
  onActivate: (index: number) => void;
  onSelect: (target: ModelTarget) => void;
  /** 顶部特殊项之后插入的「最近使用」横条；不显示时传 null。 */
  recentChipsRow: React.ReactNode;
}

export function PickerOptionList({
  listRef,
  ariaLabel,
  options,
  activeIndex,
  selected,
  scenario,
  SpecialIcon,
  syncAnchorByProvider,
  compatible,
  isProviderOnDevice,
  onSyncProvider,
  onActivate,
  onSelect,
  recentChipsRow,
}: PickerOptionListProps) {
  const { t } = useTranslation();

  return (
    <ul
      ref={listRef}
      role="listbox"
      aria-label={ariaLabel}
      className="flex flex-col gap-0.5"
    >
      {options.map((opt, i) => {
        const showGroup =
          opt.group && (i === 0 || options[i - 1].group !== opt.group);
        const showFixedSection =
          opt.kind === "fixed" &&
          (i === 0 ||
            options[i - 1].kind !== "fixed" ||
            options[i - 1].group !== opt.group);
        // 行内原因取代副行，让不可选行收敛成「名字 / 原因」两行。
        const inlineHint =
          opt.kind === "invalid"
            ? t("modelTargetPicker.invalidCurrent")
            : opt.disabled
              ? opt.disabledHint
              : undefined;
        // 行内同步入口：只挂在该供应商第一条待同步的行上。
        const syncProvider =
          syncAnchorByProvider.get(opt.target.providerKey) === opt.key
            ? compatible.find((p) => p.providerKey === opt.target.providerKey)
            : undefined;
        return (
          <React.Fragment key={opt.key}>
            <PickerOptionRow
              opt={opt}
              index={i}
              active={i === activeIndex}
              selectedNow={sameTarget(opt.target, selected)}
              showGroup={Boolean(showGroup)}
              showFixedSection={showFixedSection}
              // 远端：组头标注该供应商在目标设备上已存在（不代表模型齐全，
              // 行内另有原因）。
              groupOnDevice={isProviderOnDevice(opt.target.providerKey)}
              inlineHint={inlineHint}
              scenario={scenario}
              SpecialIcon={SpecialIcon}
              syncProvider={syncProvider}
              onSyncProvider={onSyncProvider}
              onActivate={onActivate}
              onSelect={onSelect}
            />
            {opt.kind === "special" ? recentChipsRow : null}
          </React.Fragment>
        );
      })}
    </ul>
  );
}
