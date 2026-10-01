import http from "k6/http";
import { check, sleep } from "k6";
import { Counter, Rate, Trend } from "k6/metrics";

const baseURL = (__ENV.BASE_URL || "").replace(/\/$/, "");
const n = Number(__ENV.N || 24);
const gameSeconds = Number(__ENV.GAME_SECONDS || 20);
const timeoutSeconds = Number(__ENV.TIMEOUT_SECONDS || 180);
const keys = (__ENV.API_KEYS || "").split(",").filter(Boolean);

const apiErrors = new Rate("arena_api_error");
const rateLimited = new Counter("arena_429");
const pollLatency = new Trend("arena_poll_duration", true);
const gameLatency = new Trend("arena_game_latency", true);
let arenaSnapshot = null;

export const options = {
  scenarios: {
    arena: {
      executor: "shared-iterations",
      vus: 1,
      iterations: 1,
      maxDuration: `${timeoutSeconds + 30}s`,
    },
  },
  thresholds: {
    arena_api_error: ["rate<0.01"],
  },
};

function params() {
  const headers = { "Content-Type": "application/json" };
  if (keys.length > 0) headers.Authorization = `Bearer ${keys[0]}`;
  return { headers };
}

function record(response) {
  rateLimited.add(response.status === 429);
  const ok = response.status >= 200 && response.status < 300;
  apiErrors.add(!ok && response.status !== 429);
  return ok;
}

export default function () {
  const start = http.post(
    `${baseURL}/api/arena/runs`,
    JSON.stringify({ n, duration_s: gameSeconds, player_mix: "mixed" }),
    params(),
  );
  if (!record(start) || !check(start, { "arena accepted": (r) => r.status === 202 })) return;

  const runID = start.json("run_id");
  const deadline = Date.now() + timeoutSeconds * 1000;
  while (Date.now() < deadline) {
    sleep(1);
    const response = http.get(`${baseURL}/api/arena/runs/${runID}`, params());
    pollLatency.add(response.timings.duration);
    if (!record(response)) continue;
    const snapshot = response.json();
    if (snapshot.status === "finished" || snapshot.status === "cancelled") {
      arenaSnapshot = snapshot;
      for (const entry of snapshot.leaderboard || []) {
        gameLatency.add(entry.latency_ms);
      }
      check(snapshot, {
        "arena finished": (s) => s.status === "finished",
        "all games completed": (s) => s.stats.completed === n,
      });
      return;
    }
  }
  apiErrors.add(true);
}

export function handleSummary(data) {
  const output = {
    requested_games: n,
    k6: {
      api_error_rate: data.metrics.arena_api_error?.values?.rate ?? null,
      rate_limited: data.metrics.arena_429?.values?.count ?? 0,
      poll_p50_ms: data.metrics.arena_poll_duration?.values?.med ?? null,
      poll_p95_ms: data.metrics.arena_poll_duration?.values?.["p(95)"] ?? null,
    },
    arena: arenaSnapshot,
  };
  return {
    [__ENV.RESULT_FILE || "stdout"]: `${JSON.stringify(output, null, 2)}\n`,
  };
}
