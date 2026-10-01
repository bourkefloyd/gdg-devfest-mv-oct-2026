import type { NormalizedArenaEvent, RawArenaEvent, RunCreatedResponse } from "./types";

const API_BASE = (import.meta.env.VITE_API_BASE ?? "").replace(/\/$/, "");
const EVENT_NAMES = [
  "run_started", "run-started", "game_started", "game-started", "swipe", "word",
  "word_found", "word-found", "game_updated", "game-updated", "game_finished",
  "game-finished", "stats", "snapshot", "run_finished", "run-finished", "complete", "error",
];

export async function createRun(count: number, signal?: AbortSignal): Promise<RunCreatedResponse> {
  const response = await fetch(`${API_BASE}/api/arena/runs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ count }),
    signal,
  });
  if (!response.ok) throw new Error(await apiError(response, "Unable to start arena run"));
  const body = (await response.json()) as Partial<RunCreatedResponse>;
  if (!body.run_id || typeof body.run_id !== "string") throw new Error("Arena API returned no run_id");
  return { run_id: body.run_id };
}

export async function cancelRun(runId: string): Promise<void> {
  const response = await fetch(`${API_BASE}/api/arena/runs/${encodeURIComponent(runId)}/cancel`, { method: "POST" });
  if (!response.ok) throw new Error(await apiError(response, "Unable to cancel run"));
}

export function connectRunEvents(
  runId: string,
  onEvent: (event: NormalizedArenaEvent) => void,
  onOpen: () => void,
  onDisconnect: () => void,
): () => void {
  const source = new EventSource(`${API_BASE}/api/arena/runs/${encodeURIComponent(runId)}/events`);
  source.onopen = onOpen;
  source.onerror = onDisconnect;
  const consume = (message: MessageEvent<string>) => {
    const parsed = parseArenaEvent(message.data, message.type);
    if (parsed) onEvent(parsed);
  };
  source.onmessage = consume;
  EVENT_NAMES.forEach((name) => source.addEventListener(name, consume as EventListener));
  return () => source.close();
}

export function parseArenaEvent(raw: string, eventName = "message"): NormalizedArenaEvent | null {
  try {
    const value = JSON.parse(raw) as RawArenaEvent;
    if (!value || typeof value !== "object" || Array.isArray(value)) return null;
    const nested = record(value.payload) ?? record(value.data);
    const payload = nested ?? value;
    const rawType = string(value.type) ?? string(value.event) ?? (eventName !== "message" ? eventName : undefined) ?? string(payload.type);
    if (!rawType) return null;
    return { type: rawType.toLowerCase().replace(/[.\s-]+/g, "_"), payload };
  } catch {
    return null;
  }
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function string(value: unknown): string | undefined {
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

async function apiError(response: Response, fallback: string): Promise<string> {
  try {
    const body = await response.json() as { error?: string; message?: string };
    return body.message ?? body.error ?? `${fallback} (${response.status})`;
  } catch {
    return `${fallback} (${response.status})`;
  }
}
