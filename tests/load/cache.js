import http from "k6/http";
import { check, sleep } from "k6";
import { Rate, Trend } from "k6/metrics";

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";

const cacheHitRate = new Rate("cache_hit_rate");
const cacheHitDuration = new Trend("cache_hit_duration_ms");
const cacheMissDuration = new Trend("cache_miss_duration_ms");

export const options = {
  vus: 20,
  duration: "30s",
  thresholds: {
    cache_hit_rate: ["rate>0.9"],
    cache_hit_duration_ms: ["p(95)<50"],
    http_req_failed: ["rate<0.01"],
  },
};

export default function () {
  const res = http.get(`${BASE_URL}/api/v1/search?q=cachetest`, {
    headers: { "X-API-Key": "load-test-key", "Accept-Language": "en" },
  });

  const hit = res.headers["X-Cache"] === "HIT";
  cacheHitRate.add(hit);

  if (hit) {
    cacheHitDuration.add(res.timings.duration);
  } else {
    cacheMissDuration.add(res.timings.duration);
  }

  check(res, {
    "status 200": (r) => r.status === 200,
    "response has body": (r) => r.body.length > 0,
  });

  sleep(0.05);
}
