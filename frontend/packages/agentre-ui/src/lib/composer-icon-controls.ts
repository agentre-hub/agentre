/**
 * 触屏卡片底栏的「只留图标」档。
 *
 * 手机上底栏一行要放 ＋、权限、模型、上下文、思考力度、■、↑：文字一多，模型那格
 * 是唯一能收缩的，被挤到只剩几个像素。`ChatComposer` 在触屏卡片形态给底栏打上
 * `data-controls="icons"`，各控件按这个**祖先标记**自己退成图标：
 *
 * - 文字收进 sr-only（读屏仍念得到），下拉箭头整个隐藏；
 * - 控件做成视觉 32px 的方格，透明 ::before 把点按区上下撑到 44px、左右各 4px
 *   （底栏 gap 8px，相邻两格的点按区刚好接上不重叠）。
 *
 * 标记只在触屏卡片里出现：桌面端、收起的胶囊都不带它，这些类名在那里不生效。
 * Tailwind 要在源码里看到完整类名，所以这里是写死的字面量，不要拼接。
 */
export const COMPOSER_ICON_CONTROLS = "icons";

/** 控件外壳：32px 方格 + 44px 点按区。 */
export const ICON_CONTROL_CELL =
  "in-data-[controls=icons]:relative in-data-[controls=icons]:h-8 in-data-[controls=icons]:min-w-8 in-data-[controls=icons]:justify-center in-data-[controls=icons]:rounded-lg in-data-[controls=icons]:px-2 in-data-[controls=icons]:before:absolute in-data-[controls=icons]:before:-inset-x-1 in-data-[controls=icons]:before:-inset-y-1.5";

/** 文字：收进 sr-only，读屏照念。 */
export const ICON_CONTROL_TEXT = "in-data-[controls=icons]:sr-only";

/** 纯装饰（下拉箭头等）：整个隐藏。 */
export const ICON_CONTROL_DROP = "in-data-[controls=icons]:hidden";
