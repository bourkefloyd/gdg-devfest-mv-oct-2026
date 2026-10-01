import http from "k6/http";
import { Rate, Trend } from "k6/metrics";

const baseURL = __ENV.BASE_URL || "http://127.0.0.1:8787";
const flood429 = new Rate("flood_rate_limited");
const serverErrors = new Rate("server_5xx");
const normalLatency = new Trend("normal_create_duration", true);

export const options = {
  scenarios: {
    flooder: {
      executor: "constant-arrival-rate",
      rate: Number(__ENV.FLOOD_RPS || 200),
      timeUnit: "1s",
      duration: __ENV.DURATION || "30s",
      preAllocatedVUs: 20,
      maxVUs: 200,
      exec: "flood",
    },
    normal_player: {
      executor: "constant-arrival-rate",
      rate: Number(__ENV.NORMAL_RPS || 1),
      timeUnit: "1s",
      duration: __ENV.DURATION || "30s",
      preAllocatedVUs: 2,
      maxVUs: 10,
      exec: "normal",
    },
  },
  thresholds: {
    flood_rate_limited: ["rate>0.9"],
    server_5xx: ["rate==0"],
    normal_create_duration: [`p(95)<${__ENV.NORMAL_P95_TARGET_MS || 120}`],
  },
};

function create(key, tags) {
  const response = http.post(
    `${baseURL}/v1/games`,
    JSON.stringify({ mode: "load", duration_s: 10 }),
    {
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${key}` },
      tags,
    },
  );
  serverErrors.add(response.status >= 500);
  return response;
}

export function flood() {
  const response = create(__ENV.FLOOD_KEY || "attacker-key", { actor: "flooder" });
  flood429.add(response.status === 429);
}

export function normal() {
  const response = create(__ENV.NORMAL_KEY || "load-test-key", { actor: "normal" });
  normalLatency.add(response.timings.duration);
}
