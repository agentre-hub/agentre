type FakeRequest = {
  method: string;
  path: string;
  query: Record<string, string>;
  body?: { items?: FakePushItem[] };
  identity: { userId: number; deviceId: number; fingerprint: string } | null;
};

export type FakePushItem = {
  kind: string;
  sync_id: string;
  base_version: number;
  updated_at: number;
  /** 非零 = 墓碑，值是发起端记下的删除时刻（规格 2026-08-27-schema-overhaul 决策 20）。 */
  deleted_at: number;
  payload?: Record<string, unknown>;
};

export type FakePullItem = {
  kind: string;
  sync_id: string;
  payload: Record<string, unknown>;
  /** 最后一次修改来自哪台机器（决策 14）；空串 = server 直写。 */
  origin_fingerprint: string;
  project_sync_id?: string;
  agentred_fingerprint?: string;
  updated_at?: number;
  deleted_at?: number;
};

function env(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is missing; run through e2e/run-e2e.mjs`);
  return value;
}

async function control(path: string, init: RequestInit = {}): Promise<Response> {
  return fetch(`${env("AGENTRE_E2E_SYNC_SERVER_URL")}/__control${path}`, {
    ...init,
    headers: {
      authorization: `Bearer ${env("AGENTRE_E2E_CONTROL_TOKEN")}`,
      "content-type": "application/json",
      ...init.headers,
    },
  });
}

export async function fakeSyncRequests(): Promise<FakeRequest[]> {
  const response = await control("/requests");
  if (!response.ok) throw new Error(`fake sync recorder returned HTTP ${response.status}`);
  return ((await response.json()) as { requests: FakeRequest[] }).requests;
}

export async function queueRemoteSyncItems(items: FakePullItem[]): Promise<void> {
  const response = await control("/pull-items", {
    method: "POST",
    body: JSON.stringify({ items }),
  });
  if (!response.ok) throw new Error(`fake sync queue returned HTTP ${response.status}`);
}

export async function configureSyncFaults(faults: {
  push401?: number;
  invalidPush?: number;
  invalidPull?: number;
}): Promise<void> {
  const response = await control("/faults", {
    method: "POST",
    body: JSON.stringify(faults),
  });
  if (!response.ok) throw new Error(`fake sync fault control returned HTTP ${response.status}`);
}

export function projectPushItems(requests: FakeRequest[], projectName: string): Array<{
  request: FakeRequest;
  item: FakePushItem;
}> {
  const matches: Array<{ request: FakeRequest; item: FakePushItem }> = [];
  for (const request of requests) {
    if (request.path !== "/v1/sync/push") continue;
    for (const item of request.body?.items ?? []) {
      if (item.kind === "project" && item.payload?.name === projectName) {
        matches.push({ request, item });
      }
    }
  }
  return matches;
}
