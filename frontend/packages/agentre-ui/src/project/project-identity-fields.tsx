/**
 * 身份区：字形 + 名字 + 简介，两个弹窗（设置 / 新建）共用那一份。
 *
 * 合之前这几格在两个弹窗里各写了一遍，于是分叉了：新建那一侧用现成的 `IconPicker`，
 * 设置那一侧是一个要人手打 icon key 的输入框。抽成一份就是为了不让它分叉第二次。
 *
 * **名字不再另起一行标签**：它就是这个项目的标题，摆成一行大号输入比「名字」两个字
 * 加一格更省一行、也更像它本身。标签仍在（`aria-label`），只是不占版面。
 *
 * **受控**：设置那一侧在 blur 时提交，新建那一侧只攒草稿到自己的 state —— 两种语义
 * 由调用方决定，这里不替它们选，所以只交出 `onChange` 与 `onBlur`。
 */
import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Input } from "../ui/input";
import { Textarea } from "../ui/textarea";
import { ProjectGlyphPicker } from "./project-glyph-picker";

export interface ProjectIdentityFieldsProps {
  name: string;
  description: string;
  /** 当前 icon key；解成图标是宿主的事（`renderIcon`）。 */
  icon?: string;
  /** 颜色 token，如 "agent-3"。 */
  color?: string;
  onNameChange(value: string): void;
  onDescriptionChange(value: string): void;
  onNameBlur?(): void;
  onDescriptionBlur?(): void;
  onPickIcon(iconKey: string): void;
  onPickColor(color: string): void;
  /**
   * 名字那一格的失败，紧贴输入框下面。
   *
   * 整窗级的仍然落脚部（弹窗规范 4），但**字段级的不该跟着去** —— 脚部在滚动正文
   * 的下面，而点了那一格的人视线就在那一格上。
   */
  nameError?: string | null;
  autoFocusName?: boolean;
  /** 两个弹窗在同一棵树上要分得开。 */
  testIdPrefix: string;
}

export function ProjectIdentityFields({
  name,
  description,
  icon,
  color,
  onNameChange,
  onDescriptionChange,
  onNameBlur,
  onDescriptionBlur,
  onPickIcon,
  onPickColor,
  nameError,
  autoFocusName,
  testIdPrefix,
}: ProjectIdentityFieldsProps) {
  const { t } = useUiTranslation();
  const errorId = `${testIdPrefix}-name-error`;

  return (
    <div className="flex items-start gap-3.5">
      <ProjectGlyphPicker
        name={name}
        icon={icon}
        color={color}
        onPickIcon={onPickIcon}
        onPickColor={onPickColor}
      />
      <div className="min-w-0 flex-1 space-y-1.5">
        <Input
          data-testid={`${testIdPrefix}-name`}
          autoFocus={autoFocusName}
          aria-label={t("projectSettings.field.name")}
          aria-invalid={nameError ? true : undefined}
          aria-describedby={nameError ? errorId : undefined}
          value={name}
          placeholder={t("projectSettings.identity.namePlaceholder")}
          onChange={(e) => onNameChange(e.target.value)}
          onBlur={onNameBlur}
          className={cn(
            "h-9 text-prose font-semibold",
            nameError && "border-destructive",
          )}
        />
        {nameError ? (
          <p
            id={errorId}
            role="alert"
            data-testid={errorId}
            className="text-2xs text-destructive"
          >
            {nameError}
          </p>
        ) : null}
        <Textarea
          data-testid={`${testIdPrefix}-description`}
          aria-label={t("projectSettings.field.description")}
          value={description}
          placeholder={t("projectSettings.identity.descriptionPlaceholder")}
          onChange={(e) => onDescriptionChange(e.target.value)}
          onBlur={onDescriptionBlur}
          className="min-h-[52px] text-xs"
        />
      </div>
    </div>
  );
}
