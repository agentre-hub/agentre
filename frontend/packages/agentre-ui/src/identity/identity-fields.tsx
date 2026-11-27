import type * as React from "react";

import { useUiTranslation } from "../i18n";
import { Input } from "../ui/input";
import { Textarea } from "../ui/textarea";
import { useTouch } from "../ui/touch";
import {
  IdentityMarkPicker,
  type IdentityMarkPickerProps,
} from "./identity-mark-picker";

export interface IdentityFieldsProps {
  kind: IdentityMarkPickerProps["kind"];
  name: string;
  description: string;
  mark: Omit<IdentityMarkPickerProps, "kind" | "name">;
  /** 原有的归属、徽标或保存状态，放在名称与描述之间。 */
  meta?: React.ReactNode;
  onNameChange(value: string): void;
  onNameBlur?(): void;
  onDescriptionChange(value: string): void;
  onDescriptionBlur?(): void;
  /** 字段错误紧贴名称，整窗错误仍归宿主。 */
  nameError?: string | null;
  autoFocusName?: boolean;
  testIdPrefix: string;
  /** 触屏档：描述输入 16px（iOS 聚焦 < 16px 会放大整页）。不传时跟 TouchProvider，再不然保持桌面的 12px。 */
  touch?: boolean;
}

/** 只上报变更；失焦提交、自动保存或创建时提交由宿主决定。 */
export function IdentityFields({
  kind,
  name,
  description,
  mark,
  meta,
  onNameChange,
  onNameBlur,
  onDescriptionChange,
  onDescriptionBlur,
  nameError,
  autoFocusName,
  testIdPrefix,
  touch: touchProp,
}: IdentityFieldsProps) {
  const { t } = useUiTranslation();
  const touch = useTouch(touchProp);
  const errorId = `${testIdPrefix}-name-error`;

  return (
    <div className="flex items-start gap-3.5">
      <IdentityMarkPicker {...mark} kind={kind} name={name} />
      <div className="min-w-0 flex-1 space-y-1.5">
        <Input
          data-testid={`${testIdPrefix}-name`}
          autoFocus={autoFocusName}
          aria-label={t("identityFields.name")}
          aria-invalid={nameError ? true : undefined}
          aria-describedby={nameError ? errorId : undefined}
          value={name}
          placeholder={t("identityFields.name")}
          onChange={(e) => onNameChange(e.target.value)}
          onBlur={onNameBlur}
          // 手机上 16px（text-base）：iOS 聚焦字号 < 16px 的输入会放大整页，桌面端回到 15px。
          className="h-9 text-base font-semibold md:text-prose"
        />
        {nameError ? (
          <p
            id={errorId}
            role="alert"
            data-testid={errorId}
            className="text-2xs text-destructive-text"
          >
            {nameError}
          </p>
        ) : null}
        {meta}
        <Textarea
          data-testid={`${testIdPrefix}-description`}
          aria-label={t("identityFields.description")}
          value={description}
          placeholder={t("identityFields.descriptionPlaceholder")}
          onChange={(e) => onDescriptionChange(e.target.value)}
          onBlur={onDescriptionBlur}
          // 描述保持 12px（2026-11-20 定稿）：主屏幕 App 已整体禁止缩放兜住聚焦放大，
          // 放大到 16px 只会让描述显得突兀；Safari 标签页里点它仍会放大一下，可接受。
          className={touch ? "min-h-[52px] text-base" : "min-h-[52px] text-xs"}
        />
      </div>
    </div>
  );
}
