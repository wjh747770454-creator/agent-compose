import { afterEach, describe, expect, it, vi } from "vitest";
import { z } from "zod";
import { delegate, reportDelegationTakeover } from "../src/delegate.js";

describe("delegate", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    delete process.env.AGENT_COMPOSE_RUNTIME_BASE_URL;
    delete process.env.SANDBOX_ID;
    delete process.env.CAP_TOKEN;
  });

  it("calls the authenticated sandbox delegation endpoint", async () => {
    process.env.AGENT_COMPOSE_RUNTIME_BASE_URL = "http://daemon:7420/";
    process.env.SANDBOX_ID = "sandbox-1";
    process.env.CAP_TOKEN = "cap-token";
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      childRunId: "child-1",
      parentRunId: "parent-1",
      rootRunId: "root-1",
      delegationId: "delegation-1",
      attempt: 1,
      status: "succeeded",
      output: "done",
      resultJson: "{}",
      structuredResult: { project: "移动蜜罐" },
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await delegate("project-intelligence", "analyze", {
      outputSchema: z.object({ project: z.string() }),
      idempotencyKey: "delegation-1",
      reason: "identify project",
    });

    expect(result.structuredResult).toEqual({ project: "移动蜜罐" });
    expect(fetchMock).toHaveBeenCalledOnce();
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("http://daemon:7420/api/runtime/sandboxes/sandbox-1/delegations");
    expect((init.headers as Record<string, string>)["x-capability-sandbox-token"]).toBe("cap-token");
    expect(JSON.parse(init.body as string)).toMatchObject({ targetAgent: "project-intelligence", idempotencyKey: "delegation-1" });
  });

  it("requires the runtime authentication environment", async () => {
    await expect(delegate("worker", "work")).rejects.toThrow("AGENT_COMPOSE_RUNTIME_BASE_URL");
  });

  it("reports a parent takeover for audit", async () => {
    process.env.AGENT_COMPOSE_RUNTIME_BASE_URL = "http://daemon:7420";
    process.env.SANDBOX_ID = "sandbox-1";
    process.env.CAP_TOKEN = "cap-token";
    const fetchMock = vi.fn(async () => new Response(`{"recorded":true}`, { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    await reportDelegationTakeover("delegation-1", { childRunId: "child-2", reason: "fallback completed" });
    const [url] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toContain("/delegations/delegation-1/takeover");
  });
});
