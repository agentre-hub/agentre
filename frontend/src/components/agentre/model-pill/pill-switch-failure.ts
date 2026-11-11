import i18n from "@/i18n";

import { toastFailure } from "../failure-toast";

/**
 * 输入框三颗药丸（模型 / 权限模式 / 思考强度）切换失败的同一种告知（spec 2026-11-10
 * A2）：值已回到原值，toast 标题是「没切换成功，仍是 <原值>」这句本地化事实；后端带码
 * 的那句人话当正文，机器原话只进默认折叠的「详情」。三个 hook 共用这一份，不各写一遍。
 */
export const toastPillSwitchFailed: (title: string, cause: unknown) => void =
  toastFailure;

/** 弹层里留给下次打开时看的那一行：只说事实，不贴原话（原话已在 toast 详情里）。 */
export function pillLastSwitchFailedText(): string {
  return i18n.t("chatPanel.pillSwitch.lastFailed");
}
