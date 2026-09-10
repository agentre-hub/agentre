// 设置页模块的对外出口。实现按职责住在 settings/ 下:
//
//   pages.ts       页 id 的取值域、导航分组、「还没做」的清单(纯数据与类型)
//   nav.tsx        左侧导航 + 窄屏判定
//   header.tsx     标题行(H1 + 说明 + 右侧动作槽)
//   appearance.tsx 「外观」分区
//   sections.tsx   其余各分区本体
//   page.tsx       外壳:Header + 导航 + 当前分区
//
// 这里只再导出 SettingsPage,原样保持既有对外名字 —— App、index.ts 与几个用例都从
// "./settings" 取它。
export { SettingsPage } from "./settings/page";
