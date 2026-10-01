export type PlayerId = "gemma" | "gemini" | "human";

export type RejectedWord = {
  word: string;
  reason: string;
};

export type ArenaPlayer = {
  id: PlayerId;
  name: string;
  backend: string;
  model: string;
  fallback: boolean;
  score: number;
  accepted: string[];
  rejected: RejectedWord[];
  status: "waiting" | "thinking" | "finished";
  latencyMs?: number;
};

export type ArenaGame = {
  id: string;
  tiles: string;
  endsAt: string;
  duration: number;
  mode: "race" | "human_vs_gemini";
  maxScore: number;
  players: Record<string, ArenaPlayer>;
  commentary: string[];
  status: "ready" | "running" | "finished";
};

export type ArenaEvent =
  | { type: "state"; game: ArenaGame }
  | { type: "player_started"; player: PlayerId }
  | {
      type: "word";
      player: PlayerId;
      word: string;
      accepted: boolean;
      reason?: string;
      points?: number;
      total?: number;
    }
  | {
      type: "player_result";
      player: PlayerId;
      backend?: string;
      model?: string;
      fallback?: boolean;
      latency_ms?: number;
      accepted?: string[];
      rejected?: RejectedWord[];
      score?: number;
    }
  | { type: "commentary"; text: string }
  | { type: "game_over"; max_score?: number; game?: ArenaGame };

export type CreateGameInput = {
  mode: "race" | "human_vs_gemini";
  duration_s?: number;
  seed?: number;
};

const storedAPIKey =
  typeof localStorage === "undefined"
    ? null
    : localStorage.getItem("wordhunt_api_key");
const API_KEY =
  import.meta.env.VITE_GATEWAY_API_KEY || storedAPIKey || "demo-key";
const FORCE_MOCK = import.meta.env.VITE_MOCK_API === "true";

function authHeaders() {
  return {
    Authorization: `Bearer ${API_KEY}`,
    "Content-Type": "application/json",
  };
}

function parsePlayer(raw: unknown, fallbackId: PlayerId): ArenaPlayer {
  if (typeof raw === "string") {
    const id = playerId(raw, fallbackId);
    return {
      id,
      name: title(id),
      backend: id === "gemini" ? "Vertex AI" : id === "human" ? "Human input" : "Local Metal",
      model: id === "gemini" ? "Gemini Flash" : id === "human" ? "Drag to play" : "Gemma 4 E2B",
      fallback: false,
      score: 0,
      accepted: [],
      rejected: [],
      status: "waiting",
    };
  }
  const value = (raw ?? {}) as Record<string, unknown>;
  const id = playerId(value.id ?? value.name, fallbackId);
  const status = String(value.status ?? "waiting");
  return {
    id,
    name: String(
      value.display_name ?? (id === "human" ? "You" : title(id)),
    ),
    backend: String(value.backend ?? (id === "gemini" ? "Vertex AI" : "Local Metal")),
    model: String(value.model ?? (id === "gemini" ? "Gemini 2.5 Flash" : "Gemma 4 E2B")),
    fallback: Boolean(value.fallback),
    score: Number(value.score ?? value.total ?? 0),
    accepted: strings(value.accepted ?? value.words),
    rejected: rejected(value.rejected),
    status:
      status === "playing"
        ? "thinking"
        : status === "done" || status === "error"
          ? "finished"
          : (status as ArenaPlayer["status"]),
    latencyMs: numberOrUndefined(value.latency_ms ?? value.latencyMs),
  };
}

function strings(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value
    .map((item) =>
      typeof item === "string"
        ? item
        : String((item as Record<string, unknown>)?.word ?? ""),
    )
    .filter(Boolean);
}

function rejected(value: unknown): RejectedWord[] {
  if (!Array.isArray(value)) return [];
  return value.map((item) => {
    const row = item as Record<string, unknown>;
    return {
      word: String(row.word ?? row.claim ?? ""),
      reason: String(row.reason ?? "rejected"),
    };
  });
}

function numberOrUndefined(value: unknown) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function title(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function playerId(value: unknown, fallback: PlayerId = "gemma"): PlayerId {
  const normalized = String(value ?? "").toLowerCase();
  if (normalized.includes("gemini")) return "gemini";
  if (normalized.includes("human")) return "human";
  if (normalized.includes("gemma")) return "gemma";
  return fallback;
}

export function normalizeGame(raw: unknown, input?: CreateGameInput): ArenaGame {
  const value = (raw ?? {}) as Record<string, unknown>;
  const rawPlayers = value.players;
  const players: Record<string, ArenaPlayer> = {};
  if (Array.isArray(rawPlayers)) {
    rawPlayers.forEach((player, index) => {
      const fallback = index === 0 ? "gemma" : "gemini";
      const parsed = parsePlayer(player, fallback);
      players[parsed.id] = parsed;
    });
  } else if (rawPlayers && typeof rawPlayers === "object") {
    Object.entries(rawPlayers).forEach(([key, player]) => {
      const parsed = parsePlayer(player, key.toLowerCase() as PlayerId);
      players[parsed.id] = parsed;
    });
  }
  const mode =
    (String(value.mode ?? input?.mode ?? "race") as ArenaGame["mode"]) ||
    "race";
  if (!players.gemini) players.gemini = parsePlayer({}, "gemini");
  const opponent: PlayerId = mode === "human_vs_gemini" ? "human" : "gemma";
  if (!players[opponent]) players[opponent] = parsePlayer({}, opponent);
  const duration = Number(value.duration_s ?? value.duration ?? input?.duration_s ?? 80);
  const endsAt = String(
    value.ends_at ?? new Date(Date.now() + duration * 1000).toISOString(),
  );
  return {
    id: String(value.game_id ?? value.id ?? crypto.randomUUID()),
    tiles: String(value.tiles ?? "TARESNOLDICPEAMT").replace(/[^a-z]/gi, "").slice(0, 16).toUpperCase(),
    endsAt,
    duration,
    mode,
    maxScore: Number(value.max_score ?? value.max_possible_score ?? 0),
    players,
    commentary: strings(value.commentary),
    status: value.over
      ? "finished"
      : ((value.status as ArenaGame["status"]) ?? "ready"),
  };
}

export async function createGame(input: CreateGameInput): Promise<{
  game: ArenaGame;
  mock: boolean;
}> {
  if (!FORCE_MOCK) {
    try {
      const response = await fetch("/v1/games", {
        method: "POST",
        headers: authHeaders(),
        body: JSON.stringify(input),
      });
      if (!response.ok) throw new Error(`Gateway returned ${response.status}`);
      return { game: normalizeGame(await response.json(), input), mock: false };
    } catch (error) {
      console.info("Gateway unavailable; using projector-safe mock.", error);
    }
  }
  await delay(350);
  return { game: makeMockGame(input), mock: true };
}

export async function getGame(id: string): Promise<ArenaGame> {
  const response = await fetch(`/v1/games/${id}`, { headers: authHeaders() });
  if (!response.ok) throw new Error(`Could not refresh game (${response.status})`);
  return normalizeGame(await response.json());
}

export async function submitWord(
  gameId: string,
  word: string,
  path: number[],
  mock: boolean,
) {
  if (mock) {
    await delay(120);
    const valid = ["star", "stare", "tone", "stone", "rate", "notes", "meal", "steam"];
    const accepted = valid.includes(word.toLowerCase());
    return {
      accepted,
      reason: accepted ? "" : path.length < 3 ? "too_short" : "not_a_word",
      points: accepted ? ({ 3: 100, 4: 400, 5: 800 }[word.length] ?? 1400) : 0,
    };
  }
  const response = await fetch(`/v1/games/${gameId}/words`, {
    method: "POST",
    headers: authHeaders(),
    body: JSON.stringify({ word, path }),
  });
  const body = await response.json();
  if (!response.ok) throw new Error(body?.error?.message ?? "Word rejected");
  return body as {
    accepted: boolean;
    reason?: string;
    points?: number;
    total?: number;
  };
}

export function connectEvents(
  game: ArenaGame,
  mock: boolean,
  onEvent: (event: ArenaEvent) => void,
  onConnection?: (connected: boolean) => void,
) {
  const controller = new AbortController();
  if (mock) {
    runMockEvents(game, onEvent, controller.signal);
    onConnection?.(true);
  } else {
    streamEvents(game.id, onEvent, controller.signal, onConnection);
  }
  return () => controller.abort();
}

async function streamEvents(
  id: string,
  onEvent: (event: ArenaEvent) => void,
  signal: AbortSignal,
  onConnection?: (connected: boolean) => void,
) {
  let lastEventId = "";
  let sawGameOver = false;
  while (!signal.aborted) {
    try {
      const headers: Record<string, string> = {
        Authorization: `Bearer ${API_KEY}`,
        Accept: "text/event-stream",
      };
      if (lastEventId) headers["Last-Event-ID"] = lastEventId;
      const response = await fetch(`/v1/games/${id}/events`, {
        headers,
        signal,
      });
      if (!response.ok || !response.body) {
        throw new Error(`SSE returned ${response.status}`);
      }
      onConnection?.(true);
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      while (!signal.aborted) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true }).replace(/\r\n/g, "\n");
        const messages = buffer.split("\n\n");
        buffer = messages.pop() ?? "";
        for (const message of messages) {
          let eventName = "";
          const data: string[] = [];
          message.split("\n").forEach((line) => {
            if (line.startsWith("id:")) lastEventId = line.slice(3).trim();
            if (line.startsWith("event:")) eventName = line.slice(6).trim();
            if (line.startsWith("data:")) data.push(line.slice(5).trim());
          });
          if (!data.length) continue;
          if (eventName === "game_over") sawGameOver = true;
          try {
            const payload = JSON.parse(data.join("\n")) as Record<string, unknown>;
            onEvent(normalizeArenaEvent(eventName, payload));
          } catch {
            if (eventName === "commentary") {
              onEvent({ type: "commentary", text: data.join("\n") });
            }
          }
        }
      }
      if (sawGameOver || signal.aborted) return;
      throw new Error("SSE ended before game_over");
    } catch (error) {
      if (signal.aborted) return;
      console.error("Event stream disconnected; reconnecting", error);
      onConnection?.(false);
      await mockWait(2_000, signal);
    }
  }
}

export function normalizeArenaEvent(
  eventName: string,
  payload: Record<string, unknown>,
): ArenaEvent {
  const type = String(payload.type ?? eventName ?? "state");
  if (type === "player_started") {
    return { type, player: playerId(payload.player) };
  }
  if (type === "word") {
    return {
      type,
      player: playerId(payload.player),
      word: String(payload.word ?? ""),
      accepted: Boolean(payload.accepted),
      reason: payload.reason ? String(payload.reason) : undefined,
      points: numberOrUndefined(payload.points),
      total: numberOrUndefined(payload.total),
    };
  }
  if (type === "player_result") {
    return {
      type,
      player: playerId(payload.player),
      backend: payload.backend ? String(payload.backend) : undefined,
      model: payload.model ? String(payload.model) : undefined,
      fallback:
        typeof payload.fallback === "boolean" ? payload.fallback : undefined,
      latency_ms: numberOrUndefined(payload.latency_ms),
      score: numberOrUndefined(payload.score),
      // The gateway result event carries accepted/rejected counts. Individual
      // word events and the final state carry the actual lists.
      accepted: Array.isArray(payload.accepted)
        ? strings(payload.accepted)
        : undefined,
      rejected: Array.isArray(payload.rejected)
        ? rejected(payload.rejected)
        : undefined,
    };
  }
  if (type === "commentary") {
    return { type, text: String(payload.text ?? "") };
  }
  if (type === "game_over") {
    return {
      type,
      max_score: numberOrUndefined(payload.max_score),
      game: normalizeGame(payload),
    };
  }
  return { type: "state", game: normalizeGame(payload) };
}

function makeMockGame(input: CreateGameInput): ArenaGame {
  const game = normalizeGame(
    {
      game_id: `arena-${Math.random().toString(36).slice(2, 8)}`,
      tiles: "STARNETOLOIDMEPC",
      duration_s: input.duration_s ?? 80,
      mode: input.mode,
      max_score: 19600,
      players: [
        {
          id: input.mode === "human_vs_gemini" ? "human" : "gemma",
          name: input.mode === "human_vs_gemini" ? "You" : "Gemma",
          backend: input.mode === "human_vs_gemini" ? "Human input" : "Local Metal",
          model: input.mode === "human_vs_gemini" ? "Drag to play" : "Gemma 4 E2B",
        },
        {
          id: "gemini",
          name: "Gemini",
          backend: "Vertex AI",
          model: "Gemini 2.5 Flash",
        },
      ],
    },
    input,
  );
  return game;
}

async function runMockEvents(
  game: ArenaGame,
  emit: (event: ArenaEvent) => void,
  signal: AbortSignal,
) {
  const opponent = game.mode === "human_vs_gemini" ? "human" : "gemma";
  await mockWait(650, signal);
  if (signal.aborted) return;
  emit({ type: "player_started", player: "gemini" });
  if (opponent === "gemma") emit({ type: "player_started", player: "gemma" });
  const sequence: Array<{ at: number; event: ArenaEvent }> = [
    {
      at: 450,
      event: {
        type: "commentary",
        text: "Same board, different brains. Gemini is scanning wide while local Gemma commits early.",
      },
    },
    { at: 600, event: { type: "word", player: "gemini", word: "STAR", accepted: true, points: 400 } },
    { at: 420, event: { type: "word", player: "gemma", word: "RATE", accepted: true, points: 400 } },
    { at: 380, event: { type: "word", player: "gemini", word: "STONE", accepted: true, points: 800 } },
    { at: 320, event: { type: "word", player: "gemini", word: "NOTES", accepted: true, points: 800 } },
    { at: 420, event: { type: "word", player: "gemma", word: "STARS", accepted: false, reason: "not_on_board" } },
    { at: 300, event: { type: "word", player: "gemini", word: "MEAL", accepted: true, points: 400 } },
    { at: 480, event: { type: "word", player: "gemma", word: "TONE", accepted: true, points: 400 } },
    { at: 360, event: { type: "word", player: "gemini", word: "STEAM", accepted: true, points: 800 } },
    { at: 420, event: { type: "word", player: "gemma", word: "DREAM", accepted: false, reason: "not_a_word" } },
    {
      at: 450,
      event: {
        type: "commentary",
        text: "Validation catches two optimistic Gemma claims. Gemini extends its lead with STEAM.",
      },
    },
    {
      at: 650,
      event: {
        type: "player_result",
        player: "gemini",
        backend: "Vertex AI",
        model: "Gemini 2.5 Flash",
        latency_ms: 2870,
        score: 3200,
      },
    },
    {
      at: 500,
      event: {
        type: "player_result",
        player: "gemma",
        backend: "Local Metal",
        model: "Gemma 4 E2B",
        latency_ms: 4510,
        score: 800,
      },
    },
    {
      at: 420,
      event: {
        type: "commentary",
        text: "Gemini wins the opening burst, 3,200 to 800. Every point survived server-side board and dictionary checks.",
      },
    },
    { at: 300, event: { type: "game_over", max_score: 19600 } },
  ];
  for (const item of sequence) {
    await mockWait(item.at, signal);
    if (signal.aborted) return;
    if (game.mode === "human_vs_gemini" && "player" in item.event && item.event.player === "gemma") continue;
    emit(item.event);
  }
}

function delay(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function mockWait(ms: number, signal: AbortSignal) {
  return new Promise<void>((resolve) => {
    const timer = window.setTimeout(resolve, ms);
    signal.addEventListener(
      "abort",
      () => {
        window.clearTimeout(timer);
        resolve();
      },
      { once: true },
    );
  });
}
