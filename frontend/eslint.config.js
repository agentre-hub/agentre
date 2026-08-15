import js from "@eslint/js";
import i18next from "eslint-plugin-i18next";
import prettier from "eslint-plugin-prettier/recommended";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import tseslint from "typescript-eslint";

export default tseslint.config(
  // packages/*/dist 是共享包 prepare 阶段 tsc 的产出物，与 dist / wailsjs 同属生成物。
  { ignores: ["dist", "wailsjs", "packages/*/dist"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  i18next.configs["flat/recommended"],
  {
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    rules: {
      "i18next/no-literal-string": [
        "error",
        {
          mode: "jsx-only",
          "jsx-components": {
            exclude: ["Trans", "code", "pre", "script", "style"],
          },
          "jsx-attributes": {
            include: [
              "aria-label",
              "aria-description",
              "aria-valuetext",
              "title",
              "placeholder",
              "alt",
            ],
          },
          words: {
            exclude: [
              "[0-9!-/:-@[-`{-~]+",
              "[A-Z_-]+",
              /^\p{Emoji}+$/u,
              /^[^\p{Script=Han}]*$/u,
            ],
          },
        },
      ],
      ...reactHooks.configs.recommended.rules,
      "react-refresh/only-export-components": "off",
      "@typescript-eslint/no-unused-vars": [
        "warn",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
      "react-hooks/set-state-in-effect": "off",
    },
  },
  {
    files: [
      "src/**/__tests__/**/*.{ts,tsx}",
      "src/**/*.{test,spec}.{ts,tsx}",
      // 共享包 packages/*/src 里的用例同理：测试中的中文是夹具与断言数据，
      // 不是产品文案。豁免必须跟着被豁免的代码走 —— 组件正从 src/ 搬进包，
      // 只写 src/** 等于让搬过去的用例凭空多出一条不适用的规则。
      "packages/*/src/**/__tests__/**/*.{ts,tsx}",
      "packages/*/src/**/*.{test,spec}.{ts,tsx}",
    ],
    rules: {
      "i18next/no-literal-string": "off",
    },
  },
  prettier,
);
