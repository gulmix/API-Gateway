import http from "k6/http";
import { check, sleep } from "k6";
import { Counter, Rate } from "k6/metrics";

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";

const rateLimited = new Counter("rate_limited_total");
const rateLimitRate = new Rate("rate_limit_rate");

export const options = {
  scenarios: {
    warmup: {
      executor: "constant-arrival-rate",
      rate: 50,
      timeUnit: "1m",
      duration: "10s",
      preAllocatedVUs: 5,
      tags: { phase: "warmup" },
    },
    burst: {
      executor: "constant-arrival-rate",
      rate: 300,
      timeUnit: "1m",
      duration: "20s",
      preAllocatedVUs: 10,
      startTime: "12s",
      tags: { phase: "burst" },
    },
  },
  thresholds: {
    rate_limit_rate: ["rate>0.1"],
    http_req_duration: ["p(99)<200"],
  },
};

export default function () {
  const res = http.get(`${BASE_URL}/api/v1/search`, {
    headers: { "X-API-Key": "load-test-key" },
  });

  const limited = res.status === 429;
  rateLimited.add(limited ? 1 : 0);
  rateLimitRate.add(limited);

  check(res, {
    "status is 200 or 429": (r) => r.status === 200 || r.status === 429,
    "retry-after present on 429": (r) =>
      r.status !== 429 || r.headers["Retry-After"] !== undefined,
  });

  sleep(0.1);
}
