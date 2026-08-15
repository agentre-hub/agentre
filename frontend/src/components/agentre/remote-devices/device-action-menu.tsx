// frontend/src/components/agentre/remote-devices/device-action-menu.tsx
import {
  MoreHorizontal,
  RotateCw,
  Edit3,
  Settings2,
  Trash2,
  Activity,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

type Props = {
  /** 只有真的有 LAN 地址的行才给「刷新直连」;没有地址可拨时不传。 */
  onRefresh?: () => void;
  onRename: () => void;
  /** 同上:没有直连端点就没有可配置的 TLS 信任。 */
  onEditTLS?: () => void;
  onRemove: () => void;
  onToggleProviders?: () => void;
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
