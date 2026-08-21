/// <reference types="vitest/config" />
import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const wailsAppMock = fileURLToPath(
  new URL("./src/__tests__/mocks/wailsApp.ts", import.meta.url),
);
const wailsModelsMock = fileURLToPath(
  new URL("./src/__tests__/mocks/wailsModels.ts", import.meta.url),
);
const wailsImportPrefixes = [
  "../",
  "../../",
  "../../../",
  "../../../../",
  "../../../../../",
  "@/../",
];
const wailsTestAliases = Object.fromEntries(
  wailsImportPrefixes.flatMap((prefix) => [
    [`${prefix}wailsjs/go/app/App`, wailsAppMock],
    [`${prefix}wailsjs/go/models`, wailsModelsMock],
  ]),
);

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
      // 顺序有意义：Vite 的字符串 alias 是前缀匹配（`x` 也吃 `x/sub`），
      // 所以 CSS 子路径必须排在包名之前，否则会被改写成 `…/index.ts/tokens.css`。
      // CSS 本来也不需要走 alias（node_modules 软链直指工作区源文件），
      // 这里显式列出只是为了把前缀匹配挡在门外。
      "@agentre-ai/agentre-ui/tokens.css": fileURLToPath(
        new URL("./packages/agentre-ui/styles/tokens.css", import.meta.url),
      ),
      "@agentre-ai/agentre-ui/code-highlight.css": fileURLToPath(
        new URL(
          "./packages/agentre-ui/styles/code-highlight.css",
          import.meta.url,
        ),
      ),
      "@agentre-ai/agentre-ui/base.css": fileURLToPath(
        new URL("./packages/agentre-ui/styles/base.css", import.meta.url),
      ),
      "@agentre-ai/agentre-ui/toast.css": fileURLToPath(
        new URL("./packages/agentre-ui/styles/toast.css", import.meta.url),
      ),
      // 语言包的窄入口(同样必须排在包名之前)。宿主 src/i18n/index.ts 只要
      // namespace + 资源,走这里就不会把整个 barrel 拖进来 —— 那条路径会在测试
      // setup 期把包内所有模块装进缓存,让后续 vi.mock 打不中(见 src/i18n/index.ts)。
      "@agentre-ai/agentre-ui/i18n": fileURLToPath(
        new URL("./packages/agentre-ui/src/i18n/index.ts", import.meta.url),
      ),
      // 桌面端直接吃共享包的源码，而不是 prepare 产出的 dist：否则改一行包内
      // 代码就得先 `pnpm build` 才看得到，HMR 与 vitest 全部失真。
      // agentre-server 那侧走 git 依赖，消费的仍是 dist（见包 exports）。
      "@agentre-ai/agentre-ui": fileURLToPath(
        new URL("./packages/agentre-ui/src/index.ts", import.meta.url),
      ),
    },
  },
  test: {
    environment: "happy-dom",
    maxWorkers: 2,
    setupFiles: ["./src/__tests__/setup.ts"],
    alias: {
      // wailsjs/ is gitignored and only generated during `wails build`.
      // All relative import paths that point into wailsjs/ are aliased here
      // so tests can run without a generated wailsjs/ directory.
      ...wailsTestAliases,
    },
  },
});
