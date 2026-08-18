import assert from "node:assert/strict";
import test from "node:test";
import { groupTrafficInspectionEvents } from "./traffic-inspection-model.ts";

function inspectionEvent(eventId, flowId, type, timestamp, status) {
  return {
    schema_version: 1,
    event_id: eventId,
    flow_id: flowId,
    timestamp,
    type,
    protocol: "http",
    tls: false,
    destination: "10.244.0.15:80",
    http: { version: "HTTP/1.1", method: "GET", host: "10.244.0.15", path: "/", status },
  };
}

test("groups every phase of one flow into one exchange", () => {
  const exchanges = groupTrafficInspectionEvents([
    inspectionEvent("body", "flow-1", "body", "2026-08-18T09:22:48.003Z", 200),
    inspectionEvent("response", "flow-1", "response", "2026-08-18T09:22:48.002Z", 200),
    inspectionEvent("request", "flow-1", "request", "2026-08-18T09:22:48.001Z"),
  ]);

  assert.equal(exchanges.length, 1);
  assert.equal(exchanges[0].flowId, "flow-1");
  assert.deepEqual(exchanges[0].events.map((event) => event.event_id), ["request", "response", "body"]);
  assert.equal(exchanges[0].summaryEvent.event_id, "response");
});

test("does not merge independent flows with the same host and path", () => {
  const exchanges = groupTrafficInspectionEvents([
    inspectionEvent("response-2", "flow-2", "response", "2026-08-18T09:22:49.002Z", 204),
    inspectionEvent("request-2", "flow-2", "request", "2026-08-18T09:22:49.001Z"),
    inspectionEvent("response-1", "flow-1", "response", "2026-08-18T09:22:48.002Z", 200),
    inspectionEvent("request-1", "flow-1", "request", "2026-08-18T09:22:48.001Z"),
  ]);

  assert.deepEqual(exchanges.map((exchange) => exchange.flowId), ["flow-2", "flow-1"]);
  assert.deepEqual(exchanges.map((exchange) => exchange.events.length), [2, 2]);
});
