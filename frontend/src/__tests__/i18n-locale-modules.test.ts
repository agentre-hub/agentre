import { describe, expect, it } from "vitest";
import fs from "node:fs";
import path from "node:path";

import enCommon from "@/i18n/locales/en";
import zhCommon from "@/i18n/locales/zh-CN";

// `common` 命名空间被物理拆成按功能域切分的模块文件,逻辑上仍是一棵树 ——
// 拆分本身引入了两种只在运行时才暴露的新失败模式,这里把它们钉住:
// 新增模块文件却忘了在 barrel 里合并(整块文案静默消失),以及两个模块
// 抢同一个顶层 key(后合并的那个静默覆盖前一个)。
const LOCALES_DIR = path.resolve(process.cwd(), "src/i18n/locales");
const LANGUAGES = ["en", "zh-CN"] as const;

type LocaleTree = Record<string, unknown>;

const bundles: Record<(typeof LANGUAGES)[number], LocaleTree> = {
  en: enCommon,
  "zh-CN": zhCommon,
};

function moduleFileNames(language: string): string[] {
  return fs
    .readdirSync(path.join(LOCALES_DIR, language))
    .filter((name) => name.endsWith(".json"))
    .sort();
}

function readModule(language: string, fileName: string): LocaleTree {
  return JSON.parse(
    fs.readFileSync(path.join(LOCALES_DIR, language, fileName), "utf8"),
  ) as LocaleTree;
}

describe("i18n locale modules", () => {
  it("Given locale modules split by domain, When language directories are compared, Then both languages ship the same module files", () => {
    expect(moduleFileNames("zh-CN")).toEqual(moduleFileNames("en"));
  });

  it.each(LANGUAGES)(
    "Given the %s locale modules, When their top-level keys are collected, Then no two modules claim the same key",
    (language) => {
      const owners = new Map<string, string[]>();

      for (const fileName of moduleFileNames(language)) {
        for (const key of Object.keys(readModule(language, fileName))) {
          owners.set(key, [...(owners.get(key) ?? []), fileName]);
        }
      }

      const duplicated = [...owners.entries()]
        .filter(([, files]) => files.length > 1)
        .map(([key, files]) => `${key}: ${files.join(", ")}`);

      expect(duplicated).toEqual([]);
    },
  );

  it.each(LANGUAGES)(
    "Given the %s locale barrel, When it is compared with the module files on disk, Then every module is merged in",
    (language) => {
      const onDisk = moduleFileNames(language)
        .flatMap((fileName) => Object.keys(readModule(language, fileName)))
        .sort();

      expect(Object.keys(bundles[language]).sort()).toEqual(onDisk);
    },
  );

  it.each(moduleFileNames("en"))(
    "Given the %s module, When both languages are compared, Then they carry the same top-level keys",
    (fileName) => {
      expect(Object.keys(readModule("zh-CN", fileName)).sort()).toEqual(
        Object.keys(readModule("en", fileName)).sort(),
      );
    },
  );
});
