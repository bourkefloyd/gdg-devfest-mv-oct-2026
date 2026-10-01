import http from "k6/http";
import { sleep } from "k6";
import { Counter, Rate, Trend } from "k6/metrics";

const baseURL = __ENV.BASE_URL || "http://127.0.0.1:8787";
const keys = (__ENV.API_KEYS || __ENV.API_KEY || "load-test-key").split(",");
const gameCompletion = new Trend("game_completion_seconds");
const geminiMove = new Trend("gemini_move_seconds");
const completed = new Rate("games_scored");
const userErrors = new Rate("user_visible_errors");
const fallback = new Counter("fallback_total_observed");
const gemini429 = new Counter("gemini_429_observed");
let exercised = false;

export const options = {
  scenarios: {
    real_hybrid: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        { duration: __ENV.STAGE || "30s", target: 10 },
        { duration: __ENV.STAGE || "30s", target: 25 },
        { duration: __ENV.STAGE || "30s", target: 50 },
        { duration: "10s", target: 0 },
      ],
      gracefulStop: "30s",
    },
  },
  thresholds: {
    games_scored: ["rate==1"],
    user_visible_errors: ["rate<0.01"],
    gemini_move_seconds: ["p(50)<4", "p(95)<10", "p(99)<20"],
  },
};

function params() {
  return {
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${keys[(__VU - 1) % keys.length]}`,
    },
  };
}

export default function () {
  if (exercised) {
    sleep(1);
    return;
  }
  exercised = true;

  const started = Date.now();
  const create = http.post(
    `${baseURL}/v1/games`,
    JSON.stringify({ mode: "race", duration_s: Number(__ENV.ROUND_SECONDS || 15) }),
    params(),
  );
  if (![200, 201].includes(create.status)) {
    userErrors.add(true);
    if (create.status === 429) gemini429.add(1);
    return;
  }
  const game = create.json();
  const gameID = game.game_id || game.id;

  let state;
  const timeout = Date.now() + Number(__ENV.TIMEOUT_MS || 30000);
  while (Date.now() < timeout) {
    const response = http.get(`${baseURL}/v1/games/${gameID}`, params());
    if (response.status !== 200) {
      userErrors.add(true);
      return;
    }
    state = response.json();
    if (
      ["complete", "completed", "game_over"].includes(state.status) ||
      state.game_over ||
      state.over
    ) break;
    sleep(0.25);
  }

  const hasScore =
    state &&
    (state.scoreboard || state.scores || (state.players && state.players.some((p) => p.score >= 0)));
  completed.add(Boolean(hasScore));
  userErrors.add(!hasScore);
  gameCompletion.add((Date.now() - started) / 1000);
  if (state && state.players) {
    const gemini = state.players.find((p) => p.name && p.name.toLowerCase().startsWith("gemini"));
    if (gemini && gemini.latency_ms > 0) geminiMove.add(gemini.latency_ms / 1000);
    fallback.add(state.players.filter((p) => p.fallback).length);
  }
}
