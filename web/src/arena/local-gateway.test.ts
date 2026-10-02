import { afterEach, describe, expect, it, vi } from "vitest";
import { LOCAL_DIFFUSION_ORIGIN, LOCAL_HEALTH_TIMEOUT_MS, createRun, probeLocalDiffusion } from "./api";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("local diffusion gateway", () => {
  it("keeps the cloud run on the same origin", async () => {
    const fetchMock = vi.fn(async () => Response.json({ run_id: "cloud", seed: 7, tiles: "A".repeat(16) }));
    vi.stubGlobal("fetch", fetchMock);

    await createRun(4, "mixed", 10, 7);

    expect(fetchMock).toHaveBeenCalledWith("/api/arena/runs", expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ count: 4, player_mix: "mixed", duration_s: 10, seed: 7 }),
    }));
  });

  it("posts the cloud board to the local gateway", async () => {
    const fetchMock = vi.fn(async () => Response.json({ run_id: "local", seed: 7, tiles: "ABCDEFGHIJKLMNOP" }));
    vi.stubGlobal("fetch", fetchMock);

    await createRun(4, "mixed", 30, 7, undefined, LOCAL_DIFFUSION_ORIGIN, "ABCDEFGHIJKLMNOP");

    expect(fetchMock).toHaveBeenCalledWith(`${LOCAL_DIFFUSION_ORIGIN}/api/arena/runs`, expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ count: 4, player_mix: "mixed", duration_s: 30, seed: 7, tiles: "ABCDEFGHIJKLMNOP" }),
    }));
  });

  it("treats a failed health check as offline", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => { throw new Error("connection refused"); }));
    await expect(probeLocalDiffusion()).resolves.toBe(false);
  });

  it("treats a healthy local gateway as up", async () => {
    const fetchMock = vi.fn(async () => new Response("ok", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(probeLocalDiffusion()).resolves.toBe(true);
    expect(fetchMock).toHaveBeenCalledWith(`${LOCAL_DIFFUSION_ORIGIN}/api/health`, expect.objectContaining({ method: "GET" }));
  });

  it("gives up on a slow health check", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("fetch", vi.fn((_url: string, init?: RequestInit) => new Promise((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")));
    })));
    const pending = probeLocalDiffusion();
    await vi.advanceTimersByTimeAsync(LOCAL_HEALTH_TIMEOUT_MS);
    await expect(pending).resolves.toBe(false);
  });
});
