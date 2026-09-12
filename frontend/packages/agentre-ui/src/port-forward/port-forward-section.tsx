import { Copy, ExternalLink, MoreHorizontal, Plus } from "lucide-react";
import type * as React from "react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { Switch } from "../ui/switch";

/** 地址缺席时占位的破折号。见 `address` 的注释：不编一个假地址出来。 */
const ADDRESS_PLACEHOLDER = "—";

/**
 * 一条端口映射在界面上需要的全部事实 —— host-neutral。
 *
 * 两个宿主读的是**同一台设备上的同一份声明**（映射存在被访问的那台设备上），
 * 所以这里只放声明本身加一条访问地址，不放任何宿主坐标（设备 id、会话、路由）。
 */
export interface PortForwardMappingView {
  /** 稳定标识，用于回调时告诉宿主是哪一条。宿主自己的主键原样带过来即可。 */
  id: string;
  /** 设备 `127.0.0.1` 上被转发的那个端口。 */
  port: number;
  /** 用于辨认的名称。 */
  name: string;
  /** 启用位。停用的映射保留整行，但不出现「打开」。 */
  enabled: boolean;
  /**
   * 访问地址，**可选**。
   *
   * 桌面端那条 `127.0.0.1:<端口>` 是点「打开」时才 `net.Listen` 绑出来的，端口由
   * 内核给 —— 打开之前根本没有地址，渲染任何 `127.0.0.1:xxxxx` 都是编的。控制台那条
   * `/fw/<设备>/<端口>/` 无需动作就存在，走同一个字段。
   *
   * 缺席时渲 `—` 且不出复制控件；「启用但还没点过打开」与「停用」天然共用这条路径。
   */
  address?: string;
}

export interface PortForwardSectionProps {
  /** 这台设备上的全部映射，顺序由宿主决定（包内不排序，免得两端排法分叉）。 */
  mappings: PortForwardMappingView[];
  /**
   * 设备离线 / 够不着。为真时：不出新增入口、不出「打开」——点了必然失败的入口
   * 不该渲染；已有的映射照旧列出来。
   */
  offline?: boolean;
  /** 离线说明后面那句宿主补充（相对时间之类）。相对时间格式化归宿主。 */
  offlineDetail?: string;
  /**
   * 列表下方的宿主脚注。桌面端用它讲那条本机监听的生命周期，控制台没有这一句 ——
   * 所以是宿主传进来的，包内不按宿主类型分支。
   */
  footnote?: React.ReactNode;
  /**
   * 「打开」动作的可读名。缺省用包内文案；控制台那侧是「在新标签打开」这类说法，
   * 由宿主覆盖。
   */
  openLabel?: string;
  className?: string;
  /**
   * 以下五个动作全部是宿主的副作用（Wails 绑定 / relay 请求 / 系统浏览器 / 剪贴板）。
   * **缺席即不渲染对应控件** —— 包内既有纪律：宁可没有这个控件，也不给一个按下去
   * 没反应的。
   */
  onCreate?: () => void;
  onToggleEnabled?: (mapping: PortForwardMappingView, enabled: boolean) => void;
  onRemove?: (mapping: PortForwardMappingView) => void;
  onOpen?: (mapping: PortForwardMappingView) => void;
  onCopyAddress?: (mapping: PortForwardMappingView) => void;
}

/**
 * 设备的「端口转发」小节 —— 桌面端设备行下的子块与控制台设备卡展开区共用这一份。
 *
 * 只渲染标题 + 内容：外框（控制台是节间分隔线，桌面端是 `rounded-md border` 子块）
 * 属于各自宿主的版式，由宿主包在外面。
 */
export function PortForwardSection({
  mappings,
  offline = false,
  offlineDetail,
  footnote,
  openLabel,
  className,
  onCreate,
  onToggleEnabled,
  onRemove,
  onOpen,
  onCopyAddress,
}: PortForwardSectionProps) {
  const { t } = useUiTranslation();
  const canCreate = Boolean(onCreate) && !offline;

  return (
    <section
      data-slot="port-forward-section"
      className={cn("flex flex-col gap-2", className)}
    >
      <h4 className="text-xs font-medium text-muted-foreground">
        {t("portForward.title")}
      </h4>

      {offline ? (
        <p className="text-xs text-muted-foreground">
          {t("portForward.offline")}
          {offlineDetail ? (
            <span className="ml-1 text-muted-foreground/80">
              {offlineDetail}
            </span>
          ) : null}
        </p>
      ) : null}

      {!offline && mappings.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          {t("portForward.empty")}
        </p>
      ) : null}

      {mappings.length > 0 ? (
        <ul className="flex flex-col">
          {mappings.map((entry) => (
            <PortForwardRow
              key={entry.id}
              mapping={entry}
              offline={offline}
              openLabel={openLabel}
              onToggleEnabled={onToggleEnabled}
              onRemove={onRemove}
              onOpen={onOpen}
              onCopyAddress={onCopyAddress}
            />
          ))}
        </ul>
      ) : null}

      {canCreate ? (
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className="self-start text-muted-foreground"
          onClick={onCreate}
        >
          <Plus />
          {t("portForward.addMapping")}
        </Button>
      ) : null}

      {footnote ? (
        <p className="text-2xs text-muted-foreground">{footnote}</p>
      ) : null}
    </section>
  );
}

function PortForwardRow({
  mapping,
  offline,
  openLabel,
  onToggleEnabled,
  onRemove,
  onOpen,
  onCopyAddress,
}: {
  mapping: PortForwardMappingView;
  offline: boolean;
  openLabel?: string;
} & Pick<
  PortForwardSectionProps,
  "onToggleEnabled" | "onRemove" | "onOpen" | "onCopyAddress"
>) {
  const { t } = useUiTranslation();
  const address = mapping.address?.trim();
  // 「打开」在停用行与离线设备上都不渲染：前者访问一律被拒，后者根本够不着。
  const canOpen = Boolean(onOpen) && mapping.enabled && !offline;
  const canCopy = Boolean(address) && Boolean(onCopyAddress);

  return (
    <li
      data-slot="port-forward-row"
      data-enabled={mapping.enabled}
      className="flex min-h-8 items-center gap-2 text-xs"
    >
      <span className="w-12 shrink-0 font-mono tabular-nums text-foreground">
        {mapping.port}
      </span>
      <span className="min-w-0 flex-1 truncate text-foreground">
        {mapping.name}
      </span>
      <span
        data-slot="port-forward-address"
        className="min-w-0 truncate font-mono text-muted-foreground"
      >
        {address ?? ADDRESS_PLACEHOLDER}
      </span>

      {canCopy ? (
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={t("portForward.copyAddress")}
          className="text-muted-foreground"
          onClick={() => onCopyAddress?.(mapping)}
        >
          <Copy />
        </Button>
      ) : null}

      <div className="ml-auto flex items-center gap-1">
        {canOpen ? (
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={openLabel ?? t("portForward.open")}
            className="text-muted-foreground"
            onClick={() => onOpen?.(mapping)}
          >
            <ExternalLink />
          </Button>
        ) : null}

        {onToggleEnabled ? (
          <Switch
            size="sm"
            aria-label={t("portForward.toggleEnabled")}
            checked={mapping.enabled}
            onCheckedChange={(next) => onToggleEnabled(mapping, next)}
          />
        ) : null}

        {onRemove ? (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                aria-label={t("portForward.moreActions")}
                className="text-muted-foreground"
              >
                <MoreHorizontal />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem
                variant="destructive"
                onSelect={() => onRemove(mapping)}
              >
                {t("portForward.remove")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        ) : null}
      </div>
    </li>
  );
}
