// frontend/src/components/agentre/remote-devices/device-action-menu.tsx
import {
  MoreHorizontal,
  RotateCw,
  Edit3,
  Settings2,
  Trash2,
  Activity,
  ArrowUpCircle,
  Copy,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@agentre-hub/agentre-ui";

/** 「升级 agentred」这一项的呈现,由 DeviceRow 按版本判定 + 升级状态机算出来。 */
export type UpgradeMenuItem = {
  label: string;
  disabled: boolean;
  /** 可升级时附带的目标版本,画成一枚小徽标(与行上的弱徽标同语义)。 */
  badgeVersion?: string;
  onSelect: () => void;
  /**
   * 把那条 `agentred update` 复制走。它与一键升级**始终并列**,不按状态二选一
   * (决策 18):一键升级够不着的那些时候(协议不匹配连握手都没过、机器不在线、
   * 已经点过一次还没回来)命令是唯一的出口,而入口时有时无会让人怀疑自己记错了
   * 位置。所以它是这个类型的必填字段,而不是又一个可选项 —— 画得出升级项,就一定
   * 画得出它。
   */
  onCopyCommand: () => void;
};

type Props = {
  /** 只有真的有 LAN 地址的行才给「刷新直连」;没有地址可拨时不传。 */
  onRefresh?: () => void;
  onRename: () => void;
  /** 同上:没有直连端点就没有可配置的 TLS 信任。 */
  onEditTLS?: () => void;
  onRemove: () => void;
  onToggleProviders?: () => void;
  /** 不传 = 这一行没有可升级的判据(账号独有行没有 LAN 配对,够不着 RPC)。 */
  upgrade?: UpgradeMenuItem;
};

export function DeviceActionMenu(props: Props) {
  const { t } = useTranslation();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={t("common.moreActions")}
        >
          <MoreHorizontal className="h-4 w-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {props.onRefresh ? (
          <DropdownMenuItem onSelect={props.onRefresh}>
            <RotateCw className="mr-2 h-4 w-4" />
            {t("remoteDevices.actions.refreshStatus")}
          </DropdownMenuItem>
        ) : null}
        {props.upgrade ? (
          <DropdownMenuItem
            disabled={props.upgrade.disabled}
            onSelect={props.upgrade.onSelect}
          >
            <ArrowUpCircle className="mr-2 h-4 w-4" />
            {props.upgrade.label}
            {props.upgrade.badgeVersion ? (
              <span className="ml-auto text-2xs font-semibold text-status-waiting">
                {props.upgrade.badgeVersion}
              </span>
            ) : null}
          </DropdownMenuItem>
        ) : null}
        {props.upgrade ? (
          <DropdownMenuItem onSelect={props.upgrade.onCopyCommand}>
            <Copy className="mr-2 h-4 w-4" />
            {t("remoteDevices.upgrade.action.copyCommand")}
          </DropdownMenuItem>
        ) : null}
        <DropdownMenuItem onSelect={props.onRename}>
          <Edit3 className="mr-2 h-4 w-4" />
          {t("remoteDevices.actions.rename")}
        </DropdownMenuItem>
        {props.onEditTLS ? (
          <DropdownMenuItem onSelect={props.onEditTLS}>
            <Settings2 className="mr-2 h-4 w-4" />
            {t("remoteDevices.actions.editTls")}
          </DropdownMenuItem>
        ) : null}
        {props.onToggleProviders ? (
          <DropdownMenuItem onSelect={props.onToggleProviders}>
            <Activity className="mr-2 h-4 w-4" />
            {t("remoteDevices.providers.title")}
          </DropdownMenuItem>
        ) : null}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={props.onRemove}
          className="text-destructive"
        >
          <Trash2 className="mr-2 h-4 w-4" />
          {t("remoteDevices.actions.removePairing")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
