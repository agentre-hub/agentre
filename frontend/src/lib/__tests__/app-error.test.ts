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

  // B1：组织架构写入的码（13xxx 部门 / 14xxx Agent）。
  it.each([
    ["conflict", 13001, "同级下已存在同名部门"], // DepartmentNameDuplicated
    ["conflict", 13005, "父部门不能指向自身或子部门"], // DepartmentCircularReference
    ["conflict", 14001, "已经有同名 Agent"], // AgentNameDuplicated
    ["conflict", 14011, "上级 Agent 不能指向自身或下属"], // AgentCircularReference
    ["notFound", 13000, "部门不存在"], // DepartmentNotFound
    ["notFound", 13003, "父部门不存在"], // DepartmentParentNotFound
    ["notFound", 14000, "Agent 不存在"], // AgentNotFound
    ["notFound", 14005, "指定的部门不存在"], // AgentDepartmentNotFound
    ["notFound", 14010, "指定的上级 Agent 不存在"], // AgentParentNotFound
  ] as const)(
    "Given an org write fails coded %s (%i), When classified, Then the kind follows the code",
    (kind, n, msg) => {
      expect(classifyAppError(`agentre-code:${n} ${msg}`)).toEqual({
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
