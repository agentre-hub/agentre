import * as React from "react";

import { useUiTranslation } from "../i18n";
import {
  Select,
  SelectContent,
  SelectItem,
  SELECT_NONE,
  SelectTrigger,
} from "../ui/select";

/**
 * 父项目下拉，新建与设置两个弹窗共用那一份。
 *
 * 两处此前各写了一遍同样的下拉，且都是原生 `<select>` —— 那是「包里当时没有 Select
 * 原语」的就地退让，代价在深色主题下最明显：系统控件走浏览器自己的配色，与
 * `tokens.css` 那张表无关。
 *
 * 收成一个组件而不是各自换原语，是因为这两处**除了值从哪来以外没有区别**：候选
 * 一样、「无父项目」那一项一样、层级缩进一样。留两份迟早只改一处。
 */
export interface ParentOption {
  id: string;
  name: string;
  /** 层级深度，用来画缩进；不给按 0 算。 */
  depth?: number;
}

export interface ParentSelectProps {
  /** 当前父项目 id；空串表示顶层。 */
  value: string;
  options: ParentOption[];
  /**
   * 要从候选里剔掉的那一个（通常是「它自己」）。
   *
   * 指向自己会在每一端造出一个走不完的环。后代同样不合法，但那一条由服务端判 ——
   * 禁用下拉项拦不住直接打端点。
   */
  excludeId?: string;
  onChange: (parentId: string) => void;
  "data-testid"?: string;
}

export function ParentSelect({
  value,
  options,
  excludeId,
  onChange,
  "data-testid": testId,
}: ParentSelectProps) {
  const { t } = useUiTranslation();
  const candidates = React.useMemo(
    () => options.filter((o) => o.id !== excludeId),
    [options, excludeId],
  );
  const selected = candidates.find((o) => o.id === value);

  return (
    <div>
      {/* 字段名不用 <label> 包住触发器：Radix 的触发器是一颗 <button>，被 label
          包住时点在字段名上会转发一次点击，弹层开了又立刻关。 */}
      <p className="text-xs font-medium text-foreground">
        {t("projectSettings.field.parent")}
      </p>
      <Select
        value={value || SELECT_NONE}
        onValueChange={(next) => onChange(next === SELECT_NONE ? "" : next)}
      >
        <SelectTrigger
          data-testid={testId}
          aria-label={t("projectSettings.field.parent")}
          className="mt-1 font-normal text-aux"
        >
          <span className="truncate">
            {selected?.name ?? t("projectSettings.field.noParent")}
          </span>
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={SELECT_NONE}>
            {t("projectSettings.field.noParent")}
          </SelectItem>
          {candidates.map((o) => (
            <SelectItem key={o.id} value={o.id}>
              {/* 层级用真的左内边距画。此前是 `"  ".repeat(depth)` —— 那两个空格
                  在 `<option>` 里会被折叠掉，深了几层完全看不出来。 */}
              <span
                data-depth={o.depth ?? 0}
                style={{ paddingLeft: (o.depth ?? 0) * 12 }}
                className="truncate"
              >
                {o.name}
              </span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}
