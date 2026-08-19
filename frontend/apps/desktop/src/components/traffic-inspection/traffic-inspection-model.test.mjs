import assert from "node:assert/strict";
import test from "node:test";
import {
  buildTrafficInspectionCommand,
  groupTrafficInspectionEvents,
} from "./traffic-inspection-model.ts";

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

test("exports HTTPS headers and RAW request body as curl", () => {
  const request = {
    ...inspectionEvent("request", "flow-curl", "request", "2026-08-18T09:22:48.001Z"),
    protocol: "https",
    tls: true,
    destination: "api.example.com:443",
    http: {
      version: "HTTP/1.1",
      method: "POST",
      host: "api.example.com",
      path: "/v1/books?active=true",
      request_headers: {
        Authorization: ["Bearer secret"],
        "Content-Length": ["19"],
        "Content-Type": ["application/json"],
      },
    },
  };
  const raw = `POST /v1/books?active=true HTTP/1.1\r\nHost: api.example.com\r\n\r\n{"name":"O'Reilly"}`;
  const body = {
    ...request,
    event_id: "body",
    type: "body",
    raw: {
      format: "http",
      direction: "request",
      encoding: "base64",
      data: Buffer.from(raw).toString("base64"),
      size: raw.length,
      truncated: false,
    },
  };

  assert.equal(
    buildTrafficInspectionCommand([request, body]),
    "curl -X 'POST' -H 'Authorization: Bearer secret' -H 'Content-Type: application/json' " +
      "--data-binary '{\"name\":\"O'\"'\"'Reilly\"}' 'https://api.example.com/v1/books?active=true'",
  );
});

test("exports decoded gRPC request and metadata as grpcurl", () => {
  const request = {
    ...inspectionEvent("request", "flow-grpc", "request", "2026-08-18T09:22:48.001Z"),
    protocol: "grpcs",
    tls: true,
    destination: "grpcbin.example.com:9001",
    http: {
      version: "HTTP/2.0",
      method: "POST",
      host: "grpcbin.example.com:9001",
      path: "/grpcbin.GRPCBin/Unary",
      request_headers: {
        Authorization: ["Bearer secret"],
        "Content-Type": ["application/grpc"],
        Te: ["trailers"],
      },
    },
    grpc: { service: "grpcbin.GRPCBin", method: "Unary", path: "/grpcbin.GRPCBin/Unary" },
  };
  const body = {
    ...request,
    event_id: "body",
    type: "body",
    raw: {
      format: "grpc",
      direction: "request",
      encoding: "base64",
      data: "",
      size: 0,
      truncated: false,
    },
    protobuf: {
      format: "json",
      schema: "proto",
      message_type: "grpcbin.SimpleRequest",
      data: '[{"message":"hello"}]',
    },
  };

  assert.equal(
    buildTrafficInspectionCommand([request, body]),
    "grpcurl -H 'Authorization: Bearer secret' -d '{\"message\":\"hello\"}' " +
      "'grpcbin.example.com:9001' 'grpcbin.GRPCBin/Unary'",
  );
});

test("exports plaintext gRPC without inventing protobuf fields", () => {
  const request = {
    ...inspectionEvent("request", "flow-grpc", "request", "2026-08-18T09:22:48.001Z"),
    protocol: "grpc",
    tls: false,
    destination: "grpcbin.example.com:9000",
    http: {
      version: "HTTP/2.0",
      method: "POST",
      host: "grpcbin.example.com:9000",
      path: "/grpcbin.GRPCBin/Empty",
    },
    grpc: { service: "grpcbin.GRPCBin", method: "Empty", path: "/grpcbin.GRPCBin/Empty" },
  };

  assert.equal(
    buildTrafficInspectionCommand([request]),
    "grpcurl -plaintext 'grpcbin.example.com:9000' 'grpcbin.GRPCBin/Empty'",
  );
});
