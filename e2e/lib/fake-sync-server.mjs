import { randomBytes } from "node:crypto";
import { createServer } from "node:http";

function json(response, status, body) {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(`${JSON.stringify(body)}\n`);
}

function envelope(data, code = 0, msg = "ok") {
  return { code, msg, data };
}

async function readJSON(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  if (chunks.length === 0) return {};
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

function bearer(request) {
  const value = request.headers.authorization ?? "";
  return value.startsWith("Bearer ") ? value.slice("Bearer ".length) : "";
}

function publicIdentity(identity) {
  return {
    userId: identity.userId,
    deviceId: identity.deviceId,
    fingerprint: identity.fingerprint,
  };
}

export async function startFakeSyncServer({ controlToken, identity }) {
  if (!controlToken || !identity?.refreshToken) {
    throw new Error("fake sync server requires runner control token and isolated identity");
  }

  const accessToken = `e2e-access-${randomBytes(16).toString("hex")}`;
  let currentRefreshToken = identity.refreshToken;
  let nextRefresh = 1;
  let nextVersion = 1;
  let pullItems = [];
  let faults = { push401: 0, invalidPush: 0, invalidPull: 0 };
  const requests = [];

  const server = createServer(async (request, response) => {
    const url = new URL(request.url ?? "/", "http://127.0.0.1");
    const path = url.pathname;

    try {
      if (path.startsWith("/__control/")) {
        if (bearer(request) !== controlToken) {
          json(response, 401, envelope({}, 401, "unauthorized"));
          return;
        }
        if (request.method === "GET" && path === "/__control/requests") {
          json(response, 200, { requests });
          return;
        }
        if (request.method === "POST" && path === "/__control/pull-items") {
          const body = await readJSON(request);
          const items = Array.isArray(body.items) ? body.items : [];
          const queued = items.map((item) => ({
            project_sync_id: "",
            agentred_fingerprint: "",
            updated_at: Date.now(),
            origin_fingerprint: "",
            // 墓碑是**时刻**不是布尔：时刻在桌面端库、线格式与 server 库三处本来
            // 就都是时刻（规格 2026-08-27-schema-overhaul 决策 20）。
            deleted_at: 0,
            ...item,
            version: nextVersion++,
          }));
          pullItems = [...pullItems, ...queued];
          json(response, 200, { items: queued });
          return;
        }
        if (request.method === "POST" && path === "/__control/faults") {
          const body = await readJSON(request);
          faults = {
            push401: Number(body.push401 ?? 0),
            invalidPush: Number(body.invalidPush ?? 0),
            invalidPull: Number(body.invalidPull ?? 0),
          };
          json(response, 200, { faults });
          return;
        }
        json(response, 404, envelope({}, 404, "not found"));
        return;
      }

      const body = request.method === "GET" ? undefined : await readJSON(request);
      const record = {
        method: request.method,
        path,
        query: Object.fromEntries(url.searchParams),
        body: path === "/v1/oauth/token/refresh" ? { refresh_token: "[REDACTED]" } : body,
        identity: bearer(request) === accessToken ? publicIdentity(identity) : null,
      };
      requests.push(record);

      if (request.method === "POST" && path === "/v1/oauth/token/refresh") {
        if (body?.refresh_token !== currentRefreshToken) {
          json(response, 401, envelope({}, 401, "refresh rejected"));
          return;
        }
        currentRefreshToken = `e2e-refresh-rotated-${nextRefresh++}-${randomBytes(8).toString("hex")}`;
        json(
          response,
          200,
          envelope({
            access_token: accessToken,
            refresh_token: currentRefreshToken,
            token_type: "Bearer",
            expires_in: 3600,
            refresh_expires_in: 86400,
          }),
        );
        return;
      }

      if (bearer(request) !== accessToken) {
        json(response, 401, envelope({}, 401, "unauthorized"));
        return;
      }

      if (request.method === "POST" && path === "/v1/sync/push") {
        if (faults.push401 > 0) {
          faults.push401 -= 1;
          json(response, 401, envelope({}, 401, "unauthorized"));
          return;
        }
        if (faults.invalidPush > 0) {
          faults.invalidPush -= 1;
          response.writeHead(200, { "content-type": "application/json" });
          response.end("{invalid-json");
          return;
        }
        const items = Array.isArray(body?.items) ? body.items : [];
        json(
          response,
          200,
          envelope({
            results: items.map((item) => ({
              kind: item.kind,
              sync_id: item.sync_id,
              version: nextVersion++,
              status: "accepted",
              reason: "",
            })),
          }),
        );
        return;
      }

      if (request.method === "GET" && path === "/v1/sync/pull") {
        if (faults.invalidPull > 0) {
          faults.invalidPull -= 1;
          response.writeHead(200, { "content-type": "application/json" });
          response.end("{invalid-json");
          return;
        }
        const cursor = Number(url.searchParams.get("cursor") ?? 0);
        const limit = Math.max(1, Number(url.searchParams.get("limit") ?? 200));
        const items = pullItems.filter((item) => item.version > cursor).slice(0, limit);
        const nextCursor = items.length > 0 ? items.at(-1).version : cursor;
        json(
          response,
          200,
          envelope({
            items,
            next_cursor: nextCursor,
            has_more: pullItems.some((item) => item.version > nextCursor),
          }),
        );
        return;
      }

      if (request.method === "POST" && path === "/v1/sync/local-paths") {
        json(response, 200, envelope({}));
        return;
      }

      json(response, 404, envelope({}, 404, "not found"));
    } catch (error) {
      json(response, 400, envelope({}, 400, error instanceof Error ? error.message : String(error)));
    }
  });

  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  if (!address || typeof address === "string") {
    server.close();
    throw new Error("fake sync server did not bind a loopback TCP port");
  }

  return {
    url: `http://127.0.0.1:${address.port}`,
    accessToken,
    snapshot: () => ({ requests, pendingPullItems: pullItems, faults }),
    close: () => new Promise((resolve, reject) => server.close((error) => (error ? reject(error) : resolve()))),
  };
}
