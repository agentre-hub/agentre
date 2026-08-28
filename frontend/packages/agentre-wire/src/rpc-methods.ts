import {
  create,
  fromBinary,
  toBinary,
  type DescMessage,
  type MessageInitShape,
  type MessageShape,
} from "@bufbuild/protobuf";
import * as pb from "./gen/agentre/wire/wire_pb";

export interface RpcMethodDescriptor<
  Name extends string,
  Id extends number,
  Request extends DescMessage,
  Response extends DescMessage,
> {
  readonly name: Name;
  readonly id: Id;
  readonly request: Request;
  readonly response: Response;
}

function method<
  Name extends string,
  Id extends number,
  Request extends DescMessage,
  Response extends DescMessage,
>(
  name: Name,
  id: Id,
  request: Request,
  response: Response,
): RpcMethodDescriptor<Name, Id, Request, Response> {
  return { name, id, request, response };
}

export const rpcMethods = {
  authAccount: method(
    "authAccount",
    1,
    pb.AuthAccountRequestSchema,
    pb.AuthAccountResponseSchema,
  ),
  sessionList: method(
    "sessionList",
    2,
    pb.SessionListRequestSchema,
    pb.SessionListResponseSchema,
  ),
  sessionAttach: method(
    "sessionAttach",
    3,
    pb.SessionAttachRequestSchema,
    pb.SessionAttachResponseSchema,
  ),
  sessionPull: method(
    "sessionPull",
    4,
    pb.SessionPullRequestSchema,
    pb.SessionPullResponseSchema,
  ),
  sessionPendingWaiters: method(
    "sessionPendingWaiters",
    5,
    pb.SessionPendingWaitersRequestSchema,
    pb.SessionPendingWaitersResponseSchema,
  ),
  sessionDelete: method(
    "sessionDelete",
    6,
    pb.SessionDeleteRequestSchema,
    pb.SessionDeleteResponseSchema,
  ),
  setModelTarget: method(
    "setModelTarget",
    7,
    pb.SetModelTargetRequestSchema,
    pb.SetModelTargetResponseSchema,
  ),
  runtimeCapabilities: method(
    "runtimeCapabilities",
    8,
    pb.RuntimeCapabilitiesRequestSchema,
    pb.RuntimeCapabilitiesResponseSchema,
  ),
  runtimeSteer: method(
    "runtimeSteer",
    9,
    pb.RuntimeSteerRequestSchema,
    pb.EmptySchema,
  ),
  runtimeCancelSteer: method(
    "runtimeCancelSteer",
    10,
    pb.RuntimeCancelSteerRequestSchema,
    pb.RuntimeCancelSteerResponseSchema,
  ),
  runtimeDrainPending: method(
    "runtimeDrainPending",
    11,
    pb.RuntimeDrainPendingRequestSchema,
    pb.RuntimeDrainPendingResponseSchema,
  ),
  runtimeAbort: method(
    "runtimeAbort",
    12,
    pb.RuntimeAbortRequestSchema,
    pb.RuntimeAbortResponseSchema,
  ),
  runtimeStopBackgroundTask: method(
    "runtimeStopBackgroundTask",
    13,
    pb.RuntimeStopBackgroundTaskRequestSchema,
    pb.EmptySchema,
  ),
  runtimeSetPermissionMode: method(
    "runtimeSetPermissionMode",
    14,
    pb.RuntimeSetPermissionModeRequestSchema,
    pb.EmptySchema,
  ),
  runtimeSubmitAnswer: method(
    "runtimeSubmitAnswer",
    15,
    pb.RuntimeSubmitAnswerRequestSchema,
    pb.PeerSessionControlResponseSchema,
  ),
  runtimeSubmitToolPermission: method(
    "runtimeSubmitToolPermission",
    16,
    pb.RuntimeSubmitToolPermissionRequestSchema,
    pb.PeerSessionControlResponseSchema,
  ),
  runtimeRun: method(
    "runtimeRun",
    17,
    pb.RuntimeRunRequestSchema,
    pb.RuntimeRunResponseSchema,
  ),
  runtimeGoalGet: method(
    "runtimeGoalGet",
    18,
    pb.RuntimeGoalRequestSchema,
    pb.RuntimeGoalResponseSchema,
  ),
  runtimeGoalSet: method(
    "runtimeGoalSet",
    19,
    pb.RuntimeGoalRequestSchema,
    pb.RuntimeGoalResponseSchema,
  ),
  runtimeGoalClear: method(
    "runtimeGoalClear",
    20,
    pb.RuntimeGoalRequestSchema,
    pb.RuntimeGoalClearResponseSchema,
  ),
  terminalOpen: method(
    "terminalOpen",
    21,
    pb.TerminalOpenRequestSchema,
    pb.TerminalOpenResponseSchema,
  ),
  terminalWrite: method(
    "terminalWrite",
    22,
    pb.TerminalWriteRequestSchema,
    pb.EmptySchema,
  ),
  terminalResize: method(
    "terminalResize",
    23,
    pb.TerminalResizeRequestSchema,
    pb.EmptySchema,
  ),
  terminalClose: method(
    "terminalClose",
    24,
    pb.TerminalCloseRequestSchema,
    pb.EmptySchema,
  ),
  mcpProxy: method(
    "mcpProxy",
    25,
    pb.MCPProxyRequestSchema,
    pb.MCPProxyResponseSchema,
  ),
  projectSetLocalPath: method(
    "projectSetLocalPath",
    26,
    pb.ProjectSetLocalPathRequestSchema,
    pb.ProjectLocalPathResponseSchema,
  ),
  projectClearLocalPath: method(
    "projectClearLocalPath",
    27,
    pb.ProjectClearLocalPathRequestSchema,
    pb.ProjectLocalPathResponseSchema,
  ),
  skillCatalog: method(
    "skillCatalog",
    28,
    pb.SkillCatalogRequestSchema,
    pb.SkillCatalogResponseSchema,
  ),
  remoteFsListDir: method(
    "remoteFsListDir",
    29,
    pb.RemoteFsListDirRequestSchema,
    pb.RemoteFsListDirResponseSchema,
  ),
  remoteFsMkdir: method(
    "remoteFsMkdir",
    30,
    pb.RemoteFsMkdirRequestSchema,
    pb.RemoteFsMkdirResponseSchema,
  ),
  workspaceFsListDir: method(
    "workspaceFsListDir",
    31,
    pb.WorkspaceFsListDirRequestSchema,
    pb.WorkspaceFsListDirResponseSchema,
  ),
  workspaceFsGitChanges: method(
    "workspaceFsGitChanges",
    32,
    pb.WorkspaceFsGitChangesRequestSchema,
    pb.WorkspaceFsGitChangesResponseSchema,
  ),
  workspaceFsGitBranches: method(
    "workspaceFsGitBranches",
    33,
    pb.WorkspaceFsGitBranchesRequestSchema,
    pb.WorkspaceFsGitBranchesResponseSchema,
  ),
  workspaceFsReadFile: method(
    "workspaceFsReadFile",
    34,
    pb.WorkspaceFsReadFileRequestSchema,
    pb.WorkspaceFsReadFileResponseSchema,
  ),
  workspaceFsGitFileContent: method(
    "workspaceFsGitFileContent",
    35,
    pb.WorkspaceFsGitFileContentRequestSchema,
    pb.WorkspaceFsGitFileContentResponseSchema,
  ),
  workspaceFsSearchFiles: method(
    "workspaceFsSearchFiles",
    36,
    pb.WorkspaceFsSearchFilesRequestSchema,
    pb.WorkspaceFsSearchFilesResponseSchema,
  ),
  workspaceFsGitState: method(
    "workspaceFsGitState",
    37,
    pb.WorkspaceFsGitStateRequestSchema,
    pb.WorkspaceFsGitStateResponseSchema,
  ),
  authPair: method(
    "authPair",
    38,
    pb.AuthPairRequestSchema,
    pb.AuthPairResponseSchema,
  ),
  authConnect: method(
    "authConnect",
    39,
    pb.AuthConnectRequestSchema,
    pb.AuthConnectResponseSchema,
  ),
  authRevoke: method(
    "authRevoke",
    40,
    pb.AuthRevokeRequestSchema,
    pb.AuthRevokeResponseSchema,
  ),
  llmUpsert: method(
    "llmUpsert",
    41,
    pb.LLMUpsertRequestSchema,
    pb.LLMUpsertResponseSchema,
  ),
  llmDelete: method(
    "llmDelete",
    42,
    pb.LLMDeleteRequestSchema,
    pb.LLMDeleteResponseSchema,
  ),
  llmList: method(
    "llmList",
    43,
    pb.LLMListRequestSchema,
    pb.LLMListResponseSchema,
  ),
  engineTest: method(
    "engineTest",
    44,
    pb.EngineTestRequestSchema,
    pb.EngineTestResponseSchema,
  ),
  engineDiscover: method(
    "engineDiscover",
    45,
    pb.EngineDiscoverRequestSchema,
    pb.EngineDiscoverResponseSchema,
  ),
  engineScan: method(
    "engineScan",
    46,
    pb.EngineScanRequestSchema,
    pb.EngineScanResponseSchema,
  ),
  cliResolvePath: method(
    "cliResolvePath",
    47,
    pb.CLIResolvePathRequestSchema,
    pb.CLIResolvePathResponseSchema,
  ),
  cliProbe: method(
    "cliProbe",
    48,
    pb.CLIProbeRequestSchema,
    pb.CLIProbeResponseSchema,
  ),
  healthPing: method(
    "healthPing",
    49,
    pb.HealthPingRequestSchema,
    pb.HealthPingResponseSchema,
  ),
  claudeCodeUsage: method(
    "claudeCodeUsage",
    50,
    pb.ClaudeCodeUsageRequestSchema,
    pb.ClaudeCodeUsageResponseSchema,
  ),
  skillsList: method(
    "skillsList",
    51,
    pb.SkillsListRequestSchema,
    pb.SkillsListResponseSchema,
  ),
} as const;

export type AnyRpcMethod = (typeof rpcMethods)[keyof typeof rpcMethods];
export interface TypedMethodRequestFrame {
  id: bigint;
  body: {
    case: "typedMethodRequest";
    methodId: number;
    method: AnyRpcMethod["name"];
    value: object;
  };
}
export interface TypedMethodResponseFrame {
  id: bigint;
  body: {
    case: "typedMethodResponse";
    methodId: number;
    method: AnyRpcMethod["name"];
    value: object;
  };
}
export type TypedMethodFrame =
  | TypedMethodRequestFrame
  | TypedMethodResponseFrame;
const methodsById = new Map<number, AnyRpcMethod>(
  Object.values(rpcMethods).map((value) => [value.id, value]),
);

export function encodeRpcMethodRequest<M extends AnyRpcMethod>(
  id: bigint,
  descriptor: M,
  value: MessageInitShape<M["request"]>,
): Uint8Array {
  const payload = toBinary(
    descriptor.request,
    create(descriptor.request, value),
  );
  return toBinary(
    pb.RpcFrameSchema,
    create(pb.RpcFrameSchema, {
      id,
      body: {
        case: "request",
        value: create(pb.RequestSchema, {
          methodId: descriptor.id,
          encodedPayload: payload,
        }),
      },
    }),
  );
}

export function encodeRpcMethodResponse<M extends AnyRpcMethod>(
  id: bigint,
  descriptor: M,
  value: MessageInitShape<M["response"]>,
): Uint8Array {
  const payload = toBinary(
    descriptor.response,
    create(descriptor.response, value),
  );
  return toBinary(
    pb.RpcFrameSchema,
    create(pb.RpcFrameSchema, {
      id,
      body: {
        case: "response",
        value: create(pb.ResponseSchema, {
          methodId: descriptor.id,
          encodedPayload: payload,
        }),
      },
    }),
  );
}

export function decodeRpcMethodResponse<M extends AnyRpcMethod>(
  payload: Uint8Array,
  descriptor: M,
): MessageShape<M["response"]> {
  const frame = fromBinary(pb.RpcFrameSchema, payload);
  if (
    frame.body.case !== "response" ||
    frame.body.value.methodId !== descriptor.id
  )
    throw new TypeError("wire: RPC response method ID 不匹配");
  return fromBinary(
    descriptor.response,
    frame.body.value.encodedPayload,
  ) as MessageShape<M["response"]>;
}

export function decodeTypedMethodFrame(
  frame: pb.RpcFrame,
): TypedMethodFrame | undefined {
  if (frame.body.case !== "request" && frame.body.case !== "response")
    return undefined;
  if (frame.body.value.methodId === 0) return undefined;
  const descriptor = methodsById.get(frame.body.value.methodId);
  if (descriptor === undefined)
    throw new TypeError(
      `wire: 未知 RPC method ID ${frame.body.value.methodId}`,
    );
  if (frame.body.case === "request") {
    return {
      id: frame.id,
      body: {
        case: "typedMethodRequest",
        methodId: descriptor.id,
        method: descriptor.name,
        value: fromBinary(descriptor.request, frame.body.value.encodedPayload),
      },
    };
  }
  return {
    id: frame.id,
    body: {
      case: "typedMethodResponse",
      methodId: descriptor.id,
      method: descriptor.name,
      value: fromBinary(descriptor.response, frame.body.value.encodedPayload),
    },
  };
}
