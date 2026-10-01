import { describe, expect, it } from "vitest";
import { normalizeArenaEvent, normalizeGame } from "./api";

describe("gateway contract adapter", () => {
  it("normalizes the create-game response with string player names", () => {
    const game = normalizeGame({
      game_id: "game-1",
      mode: "race",
      tiles: "ABCDEFGHIJKLMNOP",
      duration_s: 80,
      ends_at: "2026-10-01T23:15:00Z",
      players: ["gemma", "gemini"],
      stream_token: "server-only-detail",
    });

    expect(game.id).toBe("game-1");
    expect(game.tiles).toBe("ABCDEFGHIJKLMNOP");
    expect(Object.keys(game.players)).toEqual(["gemma", "gemini"]);
    expect(game.players.gemini.name).toBe("Gemini");
  });

  it("maps gateway seat names and word verdicts", () => {
    expect(
      normalizeArenaEvent("word", {
        player: "gemini-agent",
        word: "stone",
        accepted: true,
        reason: "ok",
        points: 800,
        total: 1200,
      }),
    ).toEqual({
      type: "word",
      player: "gemini",
      word: "stone",
      accepted: true,
      reason: "ok",
      points: 800,
      total: 1200,
    });
  });

  it("does not mistake player-result counts for word arrays", () => {
    expect(
      normalizeArenaEvent("player_result", {
        player: "gemma",
        backend: "local-metal",
        model: "gemma-4-e2b",
        fallback: false,
        latency_ms: 4510,
        score: 800,
        accepted: 2,
        rejected: 3,
      }),
    ).toMatchObject({
      type: "player_result",
      player: "gemma",
      accepted: undefined,
      rejected: undefined,
      score: 800,
    });
  });

  it("uses game_over as the authoritative scoreboard", () => {
    const event = normalizeArenaEvent("game_over", {
      game_id: "game-1",
      mode: "race",
      tiles: "ABCDEFGHIJKLMNOP",
      ends_at: "2026-10-01T23:15:00Z",
      over: true,
      max_score: 19600,
      players: [
        {
          name: "gemma",
          backend: "local-metal",
          model: "gemma-4-e2b",
          score: 400,
          accepted: [{ word: "rate", points: 400, reason: "ok" }],
          rejected: [
            { word: "dream", points: 0, reason: "not_on_board" },
          ],
        },
        {
          name: "gemini",
          backend: "vertex",
          model: "gemini-flash",
          score: 1200,
          accepted: [
            { word: "stone", points: 800, reason: "ok" },
            { word: "star", points: 400, reason: "ok" },
          ],
          rejected: [],
        },
      ],
    });

    expect(event.type).toBe("game_over");
    if (event.type !== "game_over") throw new Error("wrong event");
    expect(event.game?.status).toBe("finished");
    expect(event.game?.maxScore).toBe(19600);
    expect(event.game?.players.gemini.accepted).toEqual(["stone", "star"]);
    expect(event.game?.players.gemma.rejected).toEqual([
      { word: "dream", reason: "not_on_board" },
    ]);
  });
});
