import { defineConfig } from "vitest/config";

/**
 * 包自己的 `pnpm test` 入口。
 *
 * 平时这些用例被宿主 app 的 vitest 一并收走跑(根 vite.config.ts 的默认 include
 * 覆盖到 packages/),但包的 package.json 里声明了 `"test": "vitest run"` ——
 * 一个声明了却跑不起来的脚本比没有更糟,所以这里给出独立跑的配置。
 *
 * codec 是纯函数,不碰 DOM,所以用 node 环境(宿主那份是 happy-dom)。
 */
export default defineConfig({
  test: {
    include: ["src/**/*.{test,spec}.ts"],
  },
});
