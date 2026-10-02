import { describe, expect, it } from "vitest";
import { parseArenaEvent } from "./api";
import { arenaReducer, initialArenaState, modelTone } from "./reducer";

describe("arena gateway events", () => {
  it("hydrates boards, swipes, results, and final leaderboard", () => {
    let state = arenaReducer(initialArenaState, { type: "start", count: 1 });

    const started = event({
      type: "game_started",
      game: {
        id: "run-001",
        index: 0,
        tiles: "ABCDEFGHIJKLMNOP",
        player_name: "Turbo Otter 01",
      },
      stats: { running: 1 },
    });
    state = arenaReducer(state, { type: "event", event: started });
    expect(state.games[0]).toMatchObject({
      id: "run-001",
      name: "Turbo Otter 01",
      status: "running",
    });
    expect(state.games[0].board).toHaveLength(16);

    const word = event({
      type: "word",
      word_event: {
        game_id: "run-001",
        player_name: "Turbo Otter 01",
        word: "ABC",
        path: [0, 1, 2],
        points: 100,
        total: 100,
      },
      stats: { running: 1, words_per_second: 4.2 },
    });
    state = arenaReducer(state, { type: "event", event: word });
    expect(state.games[0]).toMatchObject({
      currentWord: "ABC",
      score: 100,
      words: 1,
    });
    expect(state.games[0].swipe.path).toEqual([0, 1, 2]);

    const finished = event({
      type: "game_finished",
      result: {
        game_id: "run-001",
        name: "Turbo Otter 01",
        backend: "solver-bot",
        model: "trie-dfs · paced mock",
        score: 100,
        word_count: 1,
        latency_ms: 240,
      },
    });
    state = arenaReducer(state, { type: "event", event: finished });
    expect(state.games[0]).toMatchObject({
      status: "finished",
      backend: "solver-bot",
      words: 1,
      latencyMs: 240,
    });

    const runFinished = event({
      type: "run_finished",
      stats: { running: 0, p50_latency_ms: 240, p95_latency_ms: 240 },
      leaderboard: [{
        game_id: "run-001",
        name: "Turbo Otter 01",
        backend: "solver-bot",
        model: "trie-dfs · paced mock",
        score: 100,
        word_count: 1,
        latency_ms: 240,
      }],
    });
    state = arenaReducer(state, { type: "event", event: runFinished });
    expect(state.status).toBe("finished");
    expect(state.stats).toMatchObject({ running: 0, p50LatencyMs: 240 });
  });

  it("keeps local DiffusionGemma seats in their own group", () => {
    let state = arenaReducer(initialArenaState, { type: "start", count: 1 });
    state = arenaReducer(state, { type: "local-gateway", status: "live", runId: "local-run" });
    state = arenaReducer(state, {
      type: "event",
      source: "cloud",
      event: event({
        type: "game_started",
        game: { id: "cloud-1", index: 0, tiles: "ABCDEFGHIJKLMNOP", player_name: "Turbo Otter 01", profile: "Gemma 4 26B A4B" },
      }),
    });
    state = arenaReducer(state, {
      type: "event",
      source: "local",
      event: event({
        type: "game_started",
        game: { id: "local-1", index: 0, tiles: "ABCDEFGHIJKLMNOP", profile: "DiffusionGemma 26B (local)" },
      }),
    });

    expect(state.games).toHaveLength(2);
    expect(state.games[0]).toMatchObject({ id: "cloud-1", name: "Turbo Otter 01", profile: "Gemma 4 26B A4B" });
    expect(state.games[1]).toMatchObject({
      id: "local:local-1",
      name: "DiffusionGemma 26B (local)",
      profile: "DiffusionGemma 26B (local)",
      status: "running",
    });
    expect(state.localLabel).toBe("DiffusionGemma 26B (local)");

    state = arenaReducer(state, {
      type: "event",
      source: "local",
      event: event({
        type: "word",
        word_event: { game_id: "local-1", word: "GEM", path: [0, 1, 2], points: 100, total: 100 },
      }),
    });
    expect(state.games[0].score).toBe(0);
    expect(state.games[1]).toMatchObject({ score: 100, words: 1, profile: "DiffusionGemma 26B (local)" });

    state = arenaReducer(state, {
      type: "event",
      source: "cloud",
      event: event({ type: "run_finished", stats: { running: 0, p50_latency_ms: 10 }, leaderboard: [] }),
    });
    expect(state.status).toBe("running");
    expect(state.games[1].status).toBe("running");

    state = arenaReducer(state, {
      type: "event",
      source: "local",
      event: event({
        type: "run_finished",
        leaderboard: [{ game_id: "local-1", profile: "DiffusionGemma 26B (local)", score: 100, word_count: 1 }],
      }),
    });
    expect(state.status).toBe("finished");
    expect(state.games.map((game) => game.profile)).toEqual(["Gemma 4 26B A4B", "DiffusionGemma 26B (local)"]);
    expect(modelTone(state.games[0])).toBe("gemma");
    expect(modelTone(state.games[1])).toBe("diffusiongemma-local");
  });

  it("keeps diffusiongemma-local teal and preserves profile ids through later events", () => {
    let state = arenaReducer(initialArenaState, { type: "start", count: 2 });
    state = arenaReducer(state, {
      type: "events",
      batch: [
        {
          source: "cloud",
          event: event({
            type: "game_started",
            game: { id: "g-flash", index: 0, profile: "Gemini 3.8 Flash", profile_id: "gemini", model: "gemini-3.8-flash", tiles: "ABCDEFGHIJKLMNOP" },
          }),
        },
        {
          source: "local",
          event: event({
            type: "game_started",
            game: { id: "mac-1", index: 0, profile: "DiffusionGemma 26B (local)", profile_id: "diffusiongemma-local", model: "diffusiongemma-26b", tiles: "ABCDEFGHIJKLMNOP" },
          }),
        },
      ],
    });
    state = arenaReducer(state, {
      type: "event",
      source: "local",
      event: event({ type: "word", word_event: { game_id: "mac-1", word: "GEM", path: [0, 1, 2], points: 100, total: 100 } }),
    });
    const local = state.games.find((game) => game.id === "local:mac-1");
    expect(state.games[0].profileId).toBe("gemini");
    expect(local?.profileId).toBe("diffusiongemma-local");
    expect(modelTone(state.games[0])).toBe("gemini");
    expect(local && modelTone(local)).toBe("diffusiongemma-local");
    expect(modelTone({ id: "cloud", name: "JEV", model: "gemma", profile: "Gemma Diffusion JEV", profileId: "gemma-diffusion-jev" })).toBe("gemma-diffusion-jev");
    expect(modelTone({ id: "cloud", name: "Diffusion", model: "gemma-diffusion", profile: "Gemma Diffusion", profileId: "gemma-diffusion" })).toBe("gemma-diffusion");
  });

  it("finishes the cloud run when local diffusion is offline", () => {
    let state = arenaReducer(initialArenaState, { type: "start", count: 1 });
    state = arenaReducer(state, { type: "local-gateway", status: "checking" });
    state = arenaReducer(state, {
      type: "event",
      event: event({ type: "run_finished", stats: { running: 0, p50_latency_ms: 12 }, leaderboard: [] }),
    });
    expect(state.status).toBe("running");
    state = arenaReducer(state, { type: "local-gateway", status: "offline" });
    expect(state.status).toBe("finished");
    expect(state.message).toBeUndefined();
  });

  it("applies a frame of events together", () => {
    let state = arenaReducer(initialArenaState, { type: "start", count: 1 });
    state = arenaReducer(state, {
      type: "events",
      batch: [
        { event: event({ type: "game_started", game: { id: "run-001", index: 0, player_name: "Turbo Otter 01", tiles: "ABCDEFGHIJKLMNOP" } }) },
        { event: event({ type: "word", word_event: { game_id: "run-001", word: "ABC", path: [0, 1, 2], points: 100, total: 100 } }) },
      ],
    });
    expect(state.games[0]).toMatchObject({ name: "Turbo Otter 01", score: 100, words: 1, currentWord: "ABC" });
  });
});

function event(value: Record<string, unknown>) {
  const parsed = parseArenaEvent(JSON.stringify(value));
  if (!parsed) throw new Error("event did not parse");
  return parsed;
}
