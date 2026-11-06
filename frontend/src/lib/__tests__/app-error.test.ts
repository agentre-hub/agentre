import { describe, expect, it } from "vitest";

import { classifyAppError } from "../app-error";

/**
 * 管理页错误分类（S5）。码的形状与数值是 `internal/app/coded_error.go` 与
 * `internal/pkg/code/code.go` 的 wire 契约：`agentre-code:<码> <后端本地化文案>`。
 */
describe("classifyAppError", () => {
  it.each([
    ["offline", 21000, "设备离线"], // PortForwardDeviceOffline
    ["offline", 20604, "远端设备不在线"], // RemoteFsDeviceOffline
    ["conflict", 18011, "项目下还有未归档会话"], // ProjectHasActiveSessions
    ["conflict", 18001, "同级下已存在同名项目"], // ProjectNameDuplicated
    ["notFound", 18000, "项目不存在"], // ProjectNotFound
    ["invalidFile", 20700, "文件格式不识别"], // DataBundleFormatInvalid
    ["permission", 20601, "远端权限不足"], // RemoteFsPermDenied
  ] as const)(
    "Given a bridged error coded %s (%i), When classified, Then the kind follows the code and the prefix is stripped",
    (kind, n, msg) => {
      expect(classifyAppError(new Error(`agentre-code:${n} ${msg}`))).toEqual({
        kind,
        message: msg,
        detail: undefined,
      });
    },
  );

  it("Given Wails rejects with a bare string, When classified, Then it reads the code the same way", () => {
    expect(classifyAppError("agentre-code:18000 项目不存在")).toEqual({
      kind: "notFound",
      message: "项目不存在",
      detail: undefined,
    });
  });

  it("Given a coded headline followed by a raw cause line, When classified, Then the cause goes to detail only", () => {
    const got = classifyAppError(
      new Error("agentre-code:10000 操作失败\nsql: database is locked"),
    );
    expect(got).toEqual({
      kind: "unknown",
      message: "操作失败",
      detail: "sql: database is locked",
    });
  });

  it("Given a code the classifier does not map, When classified, Then kind is unknown but the backend sentence is kept as message", () => {
    expect(
      classifyAppError(new Error("agentre-code:99999 某个以后才有的码")),
    ).toEqual({
      kind: "unknown",
      message: "某个以后才有的码",
      detail: undefined,
    });
  });

  it("Given an error without the code prefix, When classified, Then it is unknown with no body message and the raw text only as detail", () => {
    expect(
      classifyAppError(
        new Error("rpc error: code = Unavailable desc = connection refused"),
      ),
    ).toEqual({
      kind: "unknown",
      message: "",
      detail: "rpc error: code = Unavailable desc = connection refused",
    });
  });

  it("Given the prefix appears mid-string, When classified, Then it is not treated as a code", () => {
    expect(
      classifyAppError(new Error("wrapped: agentre-code:18000 项目不存在")),
    ).toEqual({
      kind: "unknown",
      message: "",
      detail: "wrapped: agentre-code:18000 项目不存在",
    });
  });

  it.each([
    ["undefined", undefined],
    ["null", null],
    ["an empty string", ""],
  ])(
    "Given a rejection with %s, When classified, Then it is unknown with neither message nor detail",
    (_label, thrown) => {
      expect(classifyAppError(thrown)).toEqual({
        kind: "unknown",
        message: "",
        detail: undefined,
      });
    },
  );

  it("Given a non-Error object carrying a message, When classified, Then its message is read", () => {
    expect(
      classifyAppError({ message: "agentre-code:20601 远端权限不足" }),
    ).toEqual({
      kind: "permission",
      message: "远端权限不足",
      detail: undefined,
    });
  });
});
