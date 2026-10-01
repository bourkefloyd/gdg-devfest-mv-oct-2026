import { describe, expect, it } from "vitest";
import { parseArenaEvent } from "./api";
import { arenaReducer, initialArenaState } from "./reducer";

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
});

function event(value: Record<string, unknown>) {
  const parsed = parseArenaEvent(JSON.stringify(value));
  if (!parsed) throw new Error("event did not parse");
  return parsed;
}
