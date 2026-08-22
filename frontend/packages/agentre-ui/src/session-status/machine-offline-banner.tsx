import { WifiOff } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { Button } from "../ui/button";
import { StatusBanner } from "./status-banner";

/**
 * 「这条对话钉住的那台机器离线了」。
 *
 * 两端唯一都成立、且都各画过一份的一档，因此文案住在这里而不是两个宿主里。
 * 合并之前两端说的不是同一件事：
 *   - 桌面端讲「为什么不会自动换机器」（上下文都在那台机器上，不会改派）
 *   - agentre-server 讲「历史还读得到、消息不会排队」
 *
 * 两句都不错，但同一个用户在两端遇到同一件事会得到两种解释。所以正文取**并集**，
 * 两半都留。
 *
 * 「最后在线」由宿主格式化好了传进来：相对时间的口径（几分钟前 / 昨天）跟着宿主
 * 自己那套走，本包不该多长出一份日期格式化，更不该为此吃一个 locale 参数。
 * 取不到就别传——不编一个时刻。
 *
 * 出口只有一个，而且是统一的那一个：「新建一个会话」。因此**按钮本身住在这里**
 * ——文案与形态两端一致，宿主只给「按下去往哪走」。（这也是它与 `StatusBanner`
 * 通用的 `action` 槽的区别：那一层还不知道自己在说哪一档，出口必须由调用方给；
 * 这一层知道。）
 *
 * 「查看设备」没有被选中：横幅刚说完「离线 · 最后在线 3小时前」，点进去看到的
 * 还是那句话——它不把人往前推。
 */
export type MachineOfflineBannerProps = {
  /** 那台机器的名字。取不到就不传，标题会退到通用说法。 */
  machineName?: string;
  /** 已经格式化好的「最后在线」相对时间。 */
  lastSeen?: string;
  /**
   * 按下「新建一个会话」时宿主要做的事。两端路由不同（桌面端就地开一条新会话，
   * web 回到它自己的新建对话流），所以本包只回调、不导航。
   */
  onStartNew: () => void;
  sticky?: boolean;
};

export function MachineOfflineBanner({
  machineName,
  lastSeen,
  onStartNew,
  sticky,
}: MachineOfflineBannerProps) {
  const { t } = useUiTranslation();
  return (
    <StatusBanner
      tone="alarm"
      sticky={sticky}
      icon={<WifiOff className="size-4" aria-hidden />}
      title={
        machineName
          ? t("sessionStatus.machineOffline.title", { machine: machineName })
          : t("sessionStatus.machineOffline.titleUnknown")
      }
      body={t("sessionStatus.machineOffline.body")}
      meta={
        lastSeen ? (
          <>
            {/* 排版符号，不是文案，因此不进 t()。 */}
            {" · "}
            <span>{t("sessionStatus.lastSeen", { time: lastSeen })}</span>
          </>
        ) : null
      }
      action={
        <Button
          type="button"
          variant="outline"
          size="sm"
          // 窄容器下动作独占一行并铺满（外壳把这一格拉成整行），宽容器下缩回小按钮。
          className="w-full @md:w-auto"
          onClick={onStartNew}
        >
          {t("sessionStatus.machineOffline.startNew")}
        </Button>
      }
    />
  );
}
