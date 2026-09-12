import {
  hasIcon,
  iconCategories as sharedIconCategories,
  iconForKey,
  iconList as sharedIconList,
  iconMeta as sharedIconMeta,
  iconsByCategory as sharedIconsByCategory,
  searchIcons as sharedSearchIcons,
} from "@agentre-hub/agentre-ui";
import type { IconCategory, IconMeta } from "@agentre-hub/agentre-ui";

import i18n from "@/i18n";

/**
 * 图标词表的桌面适配层。
 *
 * 词表本身（30 个 key 的顺序、分类、文案 key、画哪枚 lucide 图标）已搬进共享包 ——
 * 这串 key 是持久化的 `avatar_icon` 列值，两个宿主各存一份清单的后果是「一边认得、
 * 另一边渲染成空头像」。这里只做宿主那一半：把包的取值函数接到宿主唯一的那个
 * i18next 实例上。
 *
 * 仍然是**取值函数**而不是常量表：此刻直接取文案等于把语言钉死在「模块第一次被
 * import 的那一刻」，之后用户切中/英，图标选择器里的分类名与图标名仍是旧语言。
 */
function translate(key: string): string {
  return i18n.t(key);
}

export type { IconCategory, IconMeta };
export { hasIcon, iconForKey };

/** 按当前语言取整张图标表。语言切换后再调一次即得新文案。 */
export function iconList(): IconMeta[] {
  return sharedIconList(translate);
}

/** 按当前语言取分类表。 */
export function iconCategories(): { key: IconCategory; label: string }[] {
  return sharedIconCategories(translate);
}

/** 按 key 取单个图标的当前语言元数据。 */
export function iconMeta(key: string | null | undefined): IconMeta | undefined {
  return sharedIconMeta(key, translate);
}

export function searchIcons(query: string): IconMeta[] {
  return sharedSearchIcons(query, translate);
}

export function iconsByCategory(): {
  category: IconCategory;
  label: string;
  items: IconMeta[];
}[] {
  return sharedIconsByCategory(translate);
}
