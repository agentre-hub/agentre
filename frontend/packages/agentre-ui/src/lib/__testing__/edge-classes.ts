/**
 * 元素自身及其所有后代上画结构线的边宽类（含变体前缀）：`border`、`border-t`、
 * `hover:border` 等。带宽度的状态色条（`border-l-2`）不在此列。
 *
 * 无边框改版的用例拿它断言「这一块没画线」——各用例此前各抄一份，正则一改就漂。
 * 宿主那边的同名助手见 `src/__tests__/helpers/edge-classes.ts`（宿主用例不跨进包的
 * 内部目录取它）。
 */
export function edgeClasses(root: Element): string[] {
  return [root, ...Array.from(root.querySelectorAll("*"))].flatMap((el) =>
    Array.from(el.classList).filter((c) =>
      /^(?:[\w-]+:)*border(?:-[tblrxy])?$/.test(c),
    ),
  );
}
