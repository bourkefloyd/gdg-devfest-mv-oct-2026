import type { ArenaGame, ArenaSource, ArenaState, ConnectionState, LocalGatewayStatus, NormalizedArenaEvent } from "./types";

export type ArenaAction =
  | { type: "start"; count: number }
  | { type: "created"; runId: string }
  | { type: "local-gateway"; status: LocalGatewayStatus; runId?: string }
  | { type: "connection"; connection: ConnectionState }
  | { type: "event"; event: NormalizedArenaEvent; source?: ArenaSource }
  | { type: "events"; batch: Array<{ event: NormalizedArenaEvent; source?: ArenaSource }> }
  | { type: "cancelling" }
  | { type: "failure"; message: string }
  | { type: "reset" };

export const initialArenaState: ArenaState = {
  localGateway: "unknown",
  cloudSettled: false,
  requestedCount: 24,
  status: "idle",
  connection: "idle",
  games: [],
  stats: { running: 0, wordsPerSecond: 0, p50LatencyMs: 0, p95LatencyMs: 0, errors: 0, retries: 0 },
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
    case "local-gateway":
      return applyLocalGateway(state, action.status, action.runId);
    case "connection":
      return { ...state, connection: action.connection };
    case "cancelling":
      return { ...state, status: "cancelling", message: "Stopping active games…" };
    case "failure":
      return { ...state, status: "error", connection: "error", message: action.message };
    case "reset":
      return { ...initialArenaState, requestedCount: state.requestedCount };
    case "event":
      return reduceServerEvent(state, action.event, action.source ?? "cloud");
    case "events":
      return action.batch.reduce((next, item) => reduceServerEvent(next, item.event, item.source ?? "cloud"), state);
  }
}

function applyLocalGateway(state: ArenaState, status: LocalGatewayStatus, runId?: string): ArenaState {
  const next: ArenaState = { ...state, localGateway: status, localRunId: runId ?? state.localRunId };
  if ((status === "offline" || status === "finished") && state.cloudSettled && state.status === "running") {
    return finishArena(next);
  }
  return next;
}

function reduceServerEvent(state: ArenaState, event: NormalizedArenaEvent, source: ArenaSource): ArenaState {
  const { type, payload } = event;
  if (["run_started", "started"].includes(type)) {
    return { ...state, status: "running", message: undefined, startedAt: state.startedAt ?? Date.now() };
  }
  if (["snapshot", "state"].includes(type)) return applySnapshot(state, payload, source);
  if (["stats", "metrics", "run_stats"].includes(type)) {
    if (localSeatOpen(state)) return { ...state, stats: deriveStats(state.games, state) };
    return { ...state, stats: statsFrom(payload, state.stats) };
  }
  if (["run_finished", "run_cancelled", "run_complete", "complete", "finished"].includes(type)) {
    return finishSource(state, payload, source);
  }
  if (type === "error" && !gameIdentifier(payload)) {
    return { ...state, stats: { ...state.stats, errors: state.stats.errors + 1 }, message: text(payload.error) ?? text(payload.message) ?? "Arena stream reported an error" };
  }

  if (["game_started", "game_start", "game_created"].includes(type)) {
    return updateGame(state, payload, source, (game) => ({ ...hydrateGame(game, payload, source), status: "running" }));
  }
  if (["swipe", "game_swipe", "path", "path_updated"].includes(type)) {
    return updateGame(state, payload, source, (game) => ({
      ...hydrateGame(game, payload, source),
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
    return updateGame(state, payload, source, (game) => {
      const word = text(payload.word) ?? game.currentWord;
      return {
        ...hydrateGame(game, payload, source),
        status: game.status === "queued" ? "running" : game.status,
        words: number(payload.words ?? payload.word_count ?? payload.total_words) ?? game.words + (payload.accepted === false ? 0 : 1),
        score: number(payload.score ?? payload.total_score ?? payload.total) ?? game.score + (number(payload.points) ?? 0),
        currentWord: word,
        swipe: {
          path: numberArray(payload.path ?? payload.indices ?? payload.tiles) ?? game.swipe.path,
          word,
          color: text(payload.color) ?? game.swipe.color,
          updatedAt: Date.now(),
        },
      };
    });
  }
  if (["game_updated", "game_update", "progress"].includes(type)) {
    return updateGame(state, payload, source, (game) => hydrateGame(game, payload, source));
  }
  if (["game_finished", "game_complete", "game_result"].includes(type)) {
    return updateGame(state, payload, source, (game) => {
      const hydrated = hydrateGame(game, payload, source);
      return { ...hydrated, status: hydrated.error ? "error" : "finished", currentWord: "" };
    });
  }
  if (["game_error", "failed"].includes(type)) {
    const next = updateGame(state, payload, source, (game) => ({ ...hydrateGame(game, payload, source), status: "error", error: text(payload.error) ?? text(payload.message) ?? "Game failed" }));
    return { ...next, stats: { ...next.stats, errors: next.stats.errors + 1 } };
  }
  return payload.games || payload.stats ? applySnapshot(state, payload, source) : state;
}

function finishSource(state: ArenaState, payload: Record<string, unknown>, source: ArenaSource): ArenaState {
  const snap = applySnapshot(state, payload, source);
  const games = snap.games.map((game) => {
    if (isLocalSeat(game) !== (source === "local")) return game;
    return game.status === "error" ? game : { ...game, status: "finished" as const };
  });
  const cloudSettled = source === "cloud" || state.cloudSettled;
  const localGateway = source === "local" ? "finished" as const : state.localGateway;
  const localDone = localGateway === "offline" || localGateway === "finished" || localGateway === "unknown";
  const next: ArenaState = { ...snap, games, cloudSettled, localGateway, stats: deriveStats(games, snap) };
  if (cloudSettled && localDone && !localSeatOpen(state)) {
    return { ...finishArena(next), stats: { ...snap.stats, running: 0 } };
  }
  if (cloudSettled && localDone) return finishArena(next);
  return { ...next, status: state.status === "cancelling" ? "cancelling" : "running" };
}

function finishArena(state: ArenaState): ArenaState {
  return {
    ...state,
    status: "finished",
    connection: "closed",
    finishedAt: Date.now(),
    stats: { ...deriveStats(state.games, state), running: 0 },
  };
}

function localSeatOpen(state: ArenaState) {
  return state.localGateway === "checking" || state.localGateway === "live" || state.localGateway === "finished";
}

function applySnapshot(state: ArenaState, payload: Record<string, unknown>, source: ArenaSource = "cloud"): ArenaState {
  const incoming = Array.isArray(payload.games) ? payload.games : Array.isArray(payload.results) ? payload.results : undefined;
  let games = state.games;
  if (incoming) {
    games = [...state.games];
    incoming.forEach((value, index) => {
      if (!isRecord(value)) return;
      const slot = locateGame(games, value, source, index);
      const base = slot >= 0 ? games[slot] : queuedGame(games.length);
      const next = hydrateGame(base, value, source, index);
      if (slot >= 0) games[slot] = next;
      else games.push(next);
    });
  }
  const statsPayload = isRecord(payload.stats) ? payload.stats : payload;
  const labeled = rememberLocalLabel({ ...state, games }, source);
  return { ...labeled, stats: statsFrom(statsPayload, deriveStats(games, state)) };
}

function updateGame(state: ArenaState, payload: Record<string, unknown>, source: ArenaSource, update: (game: ArenaGame) => ArenaGame): ArenaState {
  const games = [...state.games];
  let index = locateGame(games, payload, source);
  if (index < 0) {
    index = games.length;
    games.push(queuedGame(index));
  }
  games[index] = update(games[index]);
  const labeled = rememberLocalLabel({ ...state, games }, source);
  return { ...labeled, stats: deriveStats(games, state) };
}

function locateGame(games: ArenaGame[], payload: Record<string, unknown>, source: ArenaSource = "cloud", fallback?: number): number {
  if (source === "local") {
    const id = localSeatId(payload, fallback);
    if (!id) return -1;
    return games.findIndex((game) => game.id === id);
  }
  const id = gameIdentifier(payload);
  if (id) {
    const match = games.findIndex((game) => game.id === id);
    if (match >= 0) return match;
  }
  const ordinal = number(payload.index ?? payload.ordinal ?? payload.game_index);
  if (ordinal !== undefined && games[ordinal] && !isLocalSeat(games[ordinal])) return ordinal;
  return fallback !== undefined && games[fallback] && !isLocalSeat(games[fallback]) ? fallback : -1;
}

function hydrateGame(game: ArenaGame, payload: Record<string, unknown>, source: ArenaSource = "cloud", fallback?: number): ArenaGame {
  const board = boardFrom(payload.board ?? payload.tiles ?? payload.letters);
  const status = text(payload.status);
  const payloadName = text(payload.name ?? payload.player_name ?? payload.agent);
  const payloadProfile = text(payload.profile);
  const localLabel = payloadProfile ?? payloadName;
  return {
    ...game,
    id: (source === "local" ? localSeatId(payload, fallback) : gameIdentifier(payload)) ?? game.id,
    ordinal: number(payload.index ?? payload.ordinal ?? payload.game_index) ?? game.ordinal,
    board: board ?? game.board,
    name: source === "local" ? (localLabel ?? game.name) : (payloadName ?? game.name),
    model: text(payload.model ?? payload.model_name) ?? game.model,
    backend: text(payload.backend ?? payload.provider) ?? game.backend,
    score: number(payload.score ?? payload.total_score) ?? game.score,
    words: number(payload.word_count ?? payload.total_words ?? payload.words) ?? game.words,
    latencyMs: number(payload.latency_ms ?? payload.latency ?? payload.duration_ms) ?? game.latencyMs,
    timeToScoreMs: number(payload.time_to_score_ms) ?? game.timeToScoreMs,
    perfectScore: number(payload.perfect_score) ?? game.perfectScore,
    profile: source === "local" ? (localLabel ?? game.profile) : (payloadProfile ?? game.profile),
    profileId: text(payload.profile_id ?? payload.profileId) ?? game.profileId,
    retries: number(payload.retries) ?? game.retries,
    error: text(payload.error) ?? game.error,
    elapsedMs: number(payload.elapsed_ms ?? payload.elapsed) ?? game.elapsedMs,
    durationMs: number(payload.duration_ms ?? payload.time_limit_ms) ?? game.durationMs,
    currentWord: text(payload.current_word ?? payload.word) ?? game.currentWord,
    status: status === "finished" || status === "complete" ? "finished" : status === "error" || status === "failed" ? "error" : status === "running" ? "running" : game.status,
  };
}

function rememberLocalLabel(state: ArenaState, source: ArenaSource): ArenaState {
  if (source !== "local" || state.localLabel) return state;
  const label = state.games.find((game) => isLocalSeat(game) && game.profile)?.profile;
  return label ? { ...state, localLabel: label } : state;
}

export function isLocalSeat(game: ArenaGame) {
  return game.id.startsWith("local:") || game.id.startsWith("local-index:");
}

export type ModelTone = "gemini" | "gemma" | "gemma-diffusion" | "gemma-diffusion-jev" | "diffusiongemma-local";

export const MODEL_TONE_COLOR: Record<ModelTone, string> = {
  gemini: "#60a5fa",
  gemma: "#c084fc",
  "gemma-diffusion": "#facc15",
  "gemma-diffusion-jev": "#fb923c",
  "diffusiongemma-local": "#5eead4",
};

export function modelTone(game: Pick<ArenaGame, "id" | "name" | "model" | "profile" | "profileId">): ModelTone | undefined {
  const id = (game.profileId ?? "").trim().toLowerCase();
  if (id === "diffusiongemma-local" || isLocalSeat(game as ArenaGame)) return "diffusiongemma-local";
  if (id === "gemma-diffusion-jev" || id === "diffusion-jev") return "gemma-diffusion-jev";
  if (id === "gemma-diffusion" || id === "diffusiongemma" || id === "diffusion") return "gemma-diffusion";
  if (id === "gemini" || id.startsWith("gemini-")) return "gemini";
  if (id === "gemma" || (id.startsWith("gemma-") && !id.includes("diffusion"))) return "gemma";

  const hay = `${game.profile ?? ""} ${game.model ?? ""} ${game.name ?? ""}`.toLowerCase();
  if (hay.includes("diffusiongemma-local") || (hay.includes("diffusion") && hay.includes("local"))) return "diffusiongemma-local";
  if (hay.includes("jev")) return "gemma-diffusion-jev";
  if (hay.includes("diffusion")) return "gemma-diffusion";
  if (hay.includes("gemini")) return "gemini";
  if (hay.includes("gemma")) return "gemma";
  return undefined;
}

function localSeatId(payload: Record<string, unknown>, fallback?: number): string | undefined {
  const id = gameIdentifier(payload);
  if (id) return `local:${id}`;
  const ordinal = number(payload.index ?? payload.ordinal ?? payload.game_index) ?? fallback;
  return ordinal === undefined ? undefined : `local-index:${ordinal}`;
}

function statsFrom(payload: Record<string, unknown>, fallback: ArenaState["stats"]): ArenaState["stats"] {
  return {
    running: number(payload.games_running ?? payload.running ?? payload.active) ?? fallback.running,
    wordsPerSecond: number(payload.words_per_second ?? payload.words_per_sec ?? payload.wps) ?? fallback.wordsPerSecond,
    p50LatencyMs: number(payload.p50_latency_ms ?? payload.p50_ms ?? payload.p50) ?? fallback.p50LatencyMs,
    p95LatencyMs: number(payload.p95_latency_ms ?? payload.p95_ms ?? payload.p95) ?? fallback.p95LatencyMs,
    errors: number(payload.errors ?? payload.error_count) ?? fallback.errors,
    retries: number(payload.retries) ?? fallback.retries,
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
    retries: games.reduce((sum, game) => sum + game.retries, 0),
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
    perfectScore: 0,
    retries: 0,
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
