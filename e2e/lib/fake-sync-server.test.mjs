import assert from "node:assert/strict";
import test from "node:test";

import { startFakeSyncServer } from "./fake-sync-server.mjs";

const controlToken = "runner-control-token";
const identity = {
  userId: 7001,
  deviceId: 7101,
  fingerprint: "e2e-device-fingerprint",
  refreshToken: "e2e-refresh-token",
};

async function control(server, path, init = {}) {
  return fetch(`${server.url}/__control${path}`, {
    ...init,
    headers: {
      authorization: `Bearer ${controlToken}`,
      "content-type": "application/json",
      ...init.headers,
    },
  });
}

async function api(server, path, init = {}) {
  return fetch(`${server.url}${path}`, {
    ...init,
    headers: {
      authorization: `Bearer ${server.accessToken}`,
      "content-type": "application/json",
      ...init.headers,
    },
  });
}

test("Given a loopback fake, when refresh, multi-item push, pull, and local-path calls arrive, then it records identity/cursors and acknowledges every incidental item", async (t) => {
  const server = await startFakeSyncServer({ controlToken, identity });
  t.after(() => server.close());

  const refresh = await fetch(`${server.url}/v1/oauth/token/refresh`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ refresh_token: identity.refreshToken }),
  });
  assert.equal(refresh.status, 200);
  const refreshBody = await refresh.json();
  assert.equal(refreshBody.data.access_token, server.accessToken);
  assert.ok(refreshBody.data.refresh_token);

  const pushed = [
    {
      kind: "agent_backend",
      sync_id: "incidental-backend",
      base_version: 0,
      updated_at: 11,
      deleted_at: 0,
      payload: { name: "incidental" },
    },
    {
      kind: "project",
      sync_id: "project-local",
      base_version: 0,
      updated_at: 12,
      deleted_at: 0,
      payload: { name: "sync-smoke-local", sort_order: 0 },
    },
  ];
  const push = await api(server, "/v1/sync/push", {
    method: "POST",
    body: JSON.stringify({ items: pushed }),
  });
  assert.equal(push.status, 200);
  const pushBody = await push.json();
  assert.equal(pushBody.data.results.length, pushed.length);
  assert.deepEqual(
    pushBody.data.results.map(({ kind, sync_id, status }) => ({ kind, sync_id, status })),
    pushed.map(({ kind, sync_id }) => ({ kind, sync_id, status: "accepted" })),
  );

  const remote = {
    kind: "project",
    sync_id: "project-remote",
    payload: {
      name: "sync-smoke-remote",
      icon: "folder",
      color: "agent-2",
      description: "from peer",
      sort_order: 3,
    },
    origin_fingerprint: "fp-peer-7202",
  };
  const queued = await control(server, "/pull-items", {
    method: "POST",
    body: JSON.stringify({ items: [remote] }),
  });
  assert.equal(queued.status, 200);
  const queuedBody = await queued.json();
  assert.ok(queuedBody.items[0].version > 0);

  const pull = await api(server, "/v1/sync/pull?cursor=0&limit=200");
  assert.equal(pull.status, 200);
  const pullBody = await pull.json();
  assert.equal(pullBody.data.items[0].sync_id, remote.sync_id);
  assert.equal(pullBody.data.items[0].origin_fingerprint, remote.origin_fingerprint);
  assert.equal(pullBody.data.next_cursor, queuedBody.items[0].version);

  const localPaths = await api(server, "/v1/sync/local-paths", {
    method: "POST",
    body: JSON.stringify({ items: [{ project_sync_id: "project-local", path: "/tmp/local" }] }),
  });
  assert.equal(localPaths.status, 200);

  const recorded = await control(server, "/requests");
  assert.equal(recorded.status, 200);
  const requests = (await recorded.json()).requests;
  const recordedPush = requests.find((request) => request.path === "/v1/sync/push");
  assert.deepEqual(recordedPush.identity, {
    userId: identity.userId,
    deviceId: identity.deviceId,
    fingerprint: identity.fingerprint,
  });
  assert.equal(recordedPush.body.items.length, 2);
  assert.equal(
    requests.find((request) => request.path === "/v1/sync/pull").query.cursor,
    "0",
  );
});

test("Given runner control authorization and configured protocol faults, when an unknown caller or sync client acts, then control is denied and two 401s or invalid JSON remain observable", async (t) => {
  const server = await startFakeSyncServer({ controlToken, identity });
  t.after(() => server.close());

  const denied = await fetch(`${server.url}/__control/requests`);
  assert.equal(denied.status, 401);

  await control(server, "/faults", {
    method: "POST",
    body: JSON.stringify({ push401: 2 }),
  });
  for (let attempt = 0; attempt < 2; attempt += 1) {
    const response = await api(server, "/v1/sync/push", {
      method: "POST",
      body: JSON.stringify({ items: [{ kind: "project", sync_id: "queued" }] }),
    });
    assert.equal(response.status, 401);
  }

  await control(server, "/faults", {
    method: "POST",
    body: JSON.stringify({ invalidPush: 1 }),
  });
  const invalid = await api(server, "/v1/sync/push", {
    method: "POST",
    body: JSON.stringify({ items: [{ kind: "project", sync_id: "still-queued" }] }),
  });
  assert.equal(invalid.status, 200);
  assert.rejects(() => invalid.json(), SyntaxError);
});
