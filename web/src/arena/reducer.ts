import type { ArenaGame, ArenaState, ConnectionState, NormalizedArenaEvent } from "./types";

export type ArenaAction =
  | { type: "start"; count: number }
  | { type: "created"; runId: string }
  | { type: "connection"; connection: ConnectionState }
  | { type: "event"; event: NormalizedArenaEvent }
  | { type: "cancelling" }
  | { type: "failure"; message: string }
  | { type: "reset" };

export const initialArenaState: ArenaState = {
  requestedCount: 24,
  status: "idle",
  connection: "idle",
  games: [],
  stats: { running: 0, wordsPerSecond: 0, p50LatencyMs: 0, p95LatencyMs: 0, errors: 0 },
};

export function arenaReducer(state: ArenaState, action: ArenaAction): ArenaState {
  switch (action.type) {
    case "start":
      return {
        ...initialArenaState,
        requestedCount: action.count,
        status: "starting",
        connection: "connecting",
        startedAt: Date.now(),
        games: Array.from({ length: action.count }, (_, index) => queuedGame(index)),
      };
    case "created":
      return { ...state, runId: action.runId, status: "running", connection: "connecting" };
    case "connection":
      return { ...state, connection: action.connection };
    case "cancelling":
      return { ...state, status: "cancelling", message: "Stopping active games…" };
    case "failure":
      return { ...state, status: "error", connection: "error", message: action.message };
    case "reset":
      return { ...initialArenaState, requestedCount: state.requestedCount };
    case "event":
      return reduceServerEvent(state, action.event);
  }
}

function reduceServerEvent(state: ArenaState, event: NormalizedArenaEvent): ArenaState {
  const { type, payload } = event;
  if (["run_started", "started"].includes(type)) {
    return { ...state, status: "running", message: undefined, startedAt: state.startedAt ?? Date.now() };
  }
  if (["snapshot", "state"].includes(type)) return applySnapshot(state, payload);
  if (["stats", "metrics", "run_stats"].includes(type)) return { ...state, stats: statsFrom(payload, state.stats) };
  if (["run_finished", "run_cancelled", "run_complete", "complete", "finished"].includes(type)) {
    const snap = applySnapshot(state, payload);
    return {
      ...snap,
      status: "finished",
      connection: "closed",
      finishedAt: Date.now(),
      games: snap.games.map((game) => game.status === "error" ? game : { ...game, status: "finished" }),
      stats: { ...snap.stats, running: 0 },
    };
  }
  if (type === "error" && !gameIdentifier(payload)) {
    return { ...state, stats: { ...state.stats, errors: state.stats.errors + 1 }, message: text(payload.error) ?? text(payload.message) ?? "Arena stream reported an error" };
  }

  if (["game_started", "game_start", "game_created"].includes(type)) {
    return updateGame(state, payload, (game) => ({ ...hydrateGame(game, payload), status: "running" }));
  }
  if (["swipe", "game_swipe", "path", "path_updated"].includes(type)) {
    return updateGame(state, payload, (game) => ({
      ...hydrateGame(game, payload),
      status: game.status === "queued" ? "running" : game.status,
      currentWord: text(payload.word) ?? text(payload.current_word) ?? game.currentWord,
      swipe: {
        path: numberArray(payload.path ?? payload.indices ?? payload.tiles) ?? game.swipe.path,
        word: text(payload.word) ?? text(payload.current_word) ?? game.swipe.word,
        color: text(payload.color) ?? game.swipe.color,
        updatedAt: Date.now(),
      },
    }));
  }
  if (["word", "word_found", "word_accepted", "score"].includes(type)) {
    return updateGame(state, payload, (game) => ({
      ...hydrateGame(game, payload),
      status: game.status === "queued" ? "running" : game.status,
      words: number(payload.words ?? payload.word_count ?? payload.total_words) ?? game.words + (payload.accepted === false ? 0 : 1),
      score: number(payload.score ?? payload.total_score ?? payload.total) ?? game.score + (number(payload.points) ?? 0),
      currentWord: text(payload.word) ?? game.currentWord,
    }));
  }
  if (["game_updated", "game_update", "progress"].includes(type)) {
    return updateGame(state, payload, (game) => hydrateGame(game, payload));
  }
  if (["game_finished", "game_complete", "game_result"].includes(type)) {
    return updateGame(state, payload, (game) => ({ ...hydrateGame(game, payload), status: "finished", currentWord: "" }));
  }
  if (["game_error", "failed"].includes(type)) {
    const next = updateGame(state, payload, (game) => ({ ...hydrateGame(game, payload), status: "error", error: text(payload.error) ?? text(payload.message) ?? "Game failed" }));
    return { ...next, stats: { ...next.stats, errors: next.stats.errors + 1 } };
  }
  return payload.games || payload.stats ? applySnapshot(state, payload) : state;
}

function applySnapshot(state: ArenaState, payload: Record<string, unknown>): ArenaState {
  const incoming = Array.isArray(payload.games) ? payload.games : Array.isArray(payload.results) ? payload.results : undefined;
  let games = state.games;
  if (incoming) {
    games = [...state.games];
    incoming.forEach((value, index) => {
      if (!isRecord(value)) return;
      const slot = locateGame(games, value, index);
      const base = slot >= 0 ? games[slot] : queuedGame(games.length);
      const next = hydrateGame(base, value);
      if (slot >= 0) games[slot] = next;
      else games.push(next);
    });
  }
  const statsPayload = isRecord(payload.stats) ? payload.stats : payload;
  return { ...state, games, stats: statsFrom(statsPayload, deriveStats(games, state)) };
}

function updateGame(state: ArenaState, payload: Record<string, unknown>, update: (game: ArenaGame) => ArenaGame): ArenaState {
  const games = [...state.games];
  let index = locateGame(games, payload);
  if (index < 0) {
    index = games.length;
    games.push(queuedGame(index));
  }
  games[index] = update(games[index]);
  return { ...state, games, stats: deriveStats(games, state) };
}

function locateGame(games: ArenaGame[], payload: Record<string, unknown>, fallback?: number): number {
  const id = gameIdentifier(payload);
  if (id) {
    const match = games.findIndex((game) => game.id === id);
    if (match >= 0) return match;
  }
  const ordinal = number(payload.index ?? payload.ordinal ?? payload.game_index);
  if (ordinal !== undefined && games[ordinal]) return ordinal;
  return fallback !== undefined && games[fallback] ? fallback : -1;
}

function hydrateGame(game: ArenaGame, payload: Record<string, unknown>): ArenaGame {
  const board = boardFrom(payload.board ?? payload.tiles ?? payload.letters);
  const status = text(payload.status);
  return {
    ...game,
    id: gameIdentifier(payload) ?? game.id,
    ordinal: number(payload.index ?? payload.ordinal ?? payload.game_index) ?? game.ordinal,
    board: board ?? game.board,
    name: text(payload.name ?? payload.player_name ?? payload.agent) ?? game.name,
    model: text(payload.model ?? payload.model_name) ?? game.model,
    backend: text(payload.backend ?? payload.provider) ?? game.backend,
    score: number(payload.score ?? payload.total_score) ?? game.score,
    words: number(payload.word_count ?? payload.total_words ?? payload.words) ?? game.words,
    latencyMs: number(payload.latency_ms ?? payload.latency ?? payload.duration_ms) ?? game.latencyMs,
    elapsedMs: number(payload.elapsed_ms ?? payload.elapsed) ?? game.elapsedMs,
    durationMs: number(payload.duration_ms ?? payload.time_limit_ms) ?? game.durationMs,
    currentWord: text(payload.current_word ?? payload.word) ?? game.currentWord,
    status: status === "finished" || status === "complete" ? "finished" : status === "error" || status === "failed" ? "error" : status === "running" ? "running" : game.status,
  };
}

function statsFrom(payload: Record<string, unknown>, fallback: ArenaState["stats"]): ArenaState["stats"] {
  return {
    running: number(payload.games_running ?? payload.running ?? payload.active) ?? fallback.running,
    wordsPerSecond: number(payload.words_per_second ?? payload.words_per_sec ?? payload.wps) ?? fallback.wordsPerSecond,
    p50LatencyMs: number(payload.p50_latency_ms ?? payload.p50_ms ?? payload.p50) ?? fallback.p50LatencyMs,
    p95LatencyMs: number(payload.p95_latency_ms ?? payload.p95_ms ?? payload.p95) ?? fallback.p95LatencyMs,
    errors: number(payload.errors ?? payload.error_count) ?? fallback.errors,
  };
}

function deriveStats(games: ArenaGame[], state: ArenaState): ArenaState["stats"] {
  const running = games.filter((game) => game.status === "running").length;
  const elapsed = Math.max(((Date.now() - (state.startedAt ?? Date.now())) / 1000), 0.1);
  const words = games.reduce((sum, game) => sum + game.words, 0);
  const latencies = games.map((game) => game.latencyMs).filter((value): value is number => value !== undefined).sort((a, b) => a - b);
  return {
    running,
    wordsPerSecond: words / elapsed,
    p50LatencyMs: percentile(latencies, 0.5) ?? state.stats.p50LatencyMs,
    p95LatencyMs: percentile(latencies, 0.95) ?? state.stats.p95LatencyMs,
    errors: games.filter((game) => game.status === "error").length,
  };
}

function queuedGame(index: number): ArenaGame {
  return {
    id: `pending-${index}`,
    ordinal: index,
    board: [],
    name: `Agent ${String(index + 1).padStart(2, "0")}`,
    model: "Awaiting model",
    backend: "—",
    score: 0,
    words: 0,
    elapsedMs: 0,
    currentWord: "",
    swipe: { path: [], word: "", updatedAt: 0 },
    status: "queued",
  };
}

function percentile(values: number[], ratio: number): number | undefined {
  if (!values.length) return undefined;
  return values[Math.min(values.length - 1, Math.max(0, Math.ceil(values.length * ratio) - 1))];
}

function gameIdentifier(payload: Record<string, unknown>): string | undefined {
  const value = payload.game_id ?? payload.gameId ?? payload.id;
  return typeof value === "string" || typeof value === "number" ? String(value) : undefined;
}
function boardFrom(value: unknown): string[] | undefined {
  if (typeof value === "string") return value.replace(/[^a-z]/gi, "").slice(0, 16).toUpperCase().split("");
  if (Array.isArray(value)) return value.slice(0, 16).map((letter) => String(letter).slice(0, 1).toUpperCase());
  return undefined;
}
function number(value: unknown): number | undefined {
  const result = typeof value === "number" ? value : typeof value === "string" && value.trim() ? Number(value) : NaN;
  return Number.isFinite(result) ? result : undefined;
}
function text(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}
function numberArray(value: unknown): number[] | undefined {
  if (!Array.isArray(value)) return undefined;
  return value.map(Number).filter((item) => Number.isInteger(item) && item >= 0 && item < 16);
}
function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value);
}
