import http from "k6/http";
import { check, sleep } from "k6";
import { Counter } from "k6/metrics";

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";

const backend1Hits = new Counter("backend1_hits");
const backend2Hits = new Counter("backend2_hits");

export const options = {
  vus: 10,
  iterations: 100,
  thresholds: {
    // Each backend should receive between 40–60 % of traffic (±10 %)
    backend1_hits: ["count>40", "count<60"],
    backend2_hits: ["count>40", "count<60"],
    http_req_failed: ["rate<0.01"],
    http_req_duration: ["p(95)<300"],
  },
};

export default function () {
  const res = http.get(`${BASE_URL}/api/v1/orders`, {
    headers: { "Content-Type": "application/json" },
  });

  check(res, { "status 200": (r) => r.status === 200 });

  const backendID = res.headers["X-Backend-Id"] || "";
  if (backendID === "backend-1") {
    backend1Hits.add(1);
  } else if (backendID === "backend-2") {
    backend2Hits.add(1);
  }

  sleep(0.05);
}
