export type ConnectionState = "idle" | "connecting" | "live" | "reconnecting" | "closed" | "error";
export type RunStatus = "idle" | "starting" | "running" | "cancelling" | "finished" | "error";
export type GameStatus = "queued" | "running" | "finished" | "error";

export interface SwipeState {
  path: number[];
  word: string;
  color?: string;
  updatedAt: number;
}

export interface ArenaGame {
  id: string;
  ordinal: number;
  board: string[];
  name: string;
  model: string;
  backend: string;
  score: number;
  words: number;
  latencyMs?: number;
  timeToScoreMs?: number;
  perfectScore: number;
  elapsedMs: number;
  durationMs?: number;
  currentWord: string;
  swipe: SwipeState;
  status: GameStatus;
  error?: string;
}

export interface ArenaStats {
  running: number;
  wordsPerSecond: number;
  p50LatencyMs: number;
  p95LatencyMs: number;
  errors: number;
}

export interface ArenaState {
  runId?: string;
  requestedCount: number;
  status: RunStatus;
  connection: ConnectionState;
  games: ArenaGame[];
  stats: ArenaStats;
  message?: string;
  startedAt?: number;
  finishedAt?: number;
}

export interface RawArenaEvent {
  type?: string;
  payload?: unknown;
  event?: string;
  data?: unknown;
  [key: string]: unknown;
}

export interface NormalizedArenaEvent {
  type: string;
  payload: Record<string, unknown>;
}

export interface RunCreatedResponse {
  run_id: string;
  seed: number;
  tiles: string;
  player_mix: "bots" | "gemini" | "mixed";
}
