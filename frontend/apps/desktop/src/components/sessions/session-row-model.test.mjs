import assert from "node:assert/strict";
import test from "node:test";
import {
  sessionTargetType,
  targetWithPorts,
  targetTypeLabel,
  trafficBindingDetails,
} from "./session-row-model.ts";

test("distinguishes Pod and Service session targets", () => {
  const binding = {
    id: "task-1",
    name: "binding-1",
    namespace: "development",
    sessionId: "session-1",
    mode: "PortForward",
    desiredState: "Active",
    phase: "Ready",
    target: { kind: "Pod", name: "api" },
    ports: [{ targetPort: 80, protocol: "TCP" }],
    createdAt: "2026-09-01T00:00:00Z",
  };

  assert.equal(sessionTargetType(binding), "Pod");
  assert.equal(sessionTargetType({ ...binding, target: { kind: "Service", name: "api" } }), "Service");
  assert.equal(sessionTargetType({ ...binding, mode: "Exchange" }), "Service");
  assert.equal(targetTypeLabel("pod"), "Pod");
  assert.equal(targetTypeLabel(undefined), "—");
});

test("adds every distinct port to the session target", () => {
  assert.equal(targetWithPorts("api", [80]), "api:80");
  assert.equal(targetWithPorts("api", [80, 443, 80]), "api:80,443");
  assert.equal(targetWithPorts("preview", []), "preview:—");
});

test("shows the complete reverse traffic path for every port", () => {
  const details = trafficBindingDetails({
    id: "task-1",
    name: "binding-1",
    namespace: "development",
    sessionId: "session-1",
    mode: "Exchange",
    desiredState: "Active",
    phase: "Ready",
    target: { kind: "Service", name: "api" },
    relay: { address: "10.244.1.220" },
    serviceClusterIp: "10.96.0.15",
    ports: [
      {
        name: "http",
        targetPort: 80,
        relayPort: 41445,
        localHost: "127.0.0.1",
        localPort: 8000,
        protocol: "TCP",
      },
      {
        name: "grpc",
        targetPort: 9000,
        relayPort: 41446,
        localHost: "::1",
        localPort: 9001,
        protocol: "TCP",
      },
    ],
    createdAt: "2026-09-01T00:00:00Z",
  });

  assert.deepEqual(details, [
    { local: "127.0.0.1:8000", remote: "10.96.0.15:80", flow: "bidirectional" },
    { local: "[::1]:9001", remote: "10.96.0.15:9000", flow: "bidirectional" },
  ]);
});

test("shows the local listener and target for port forwarding", () => {
  const binding = {
    id: "task-1",
    name: "binding-1",
    namespace: "development",
    sessionId: "session-1",
    mode: "PortForward",
    desiredState: "Active",
    phase: "Ready",
    target: { kind: "Pod", name: "api" },
    ports: [{ targetPort: 80, protocol: "TCP" }],
    dialAddress: "10.244.1.200:80",
    createdAt: "2026-09-01T00:00:00Z",
  };
  const details = trafficBindingDetails(binding, {
    id: "task-1",
    profileId: "profile-1",
    sessionId: "session-1",
    namespace: "development",
    kind: "pod",
    name: "api",
    protocol: "tcp",
    remotePort: 8080,
    localPort: 18080,
    address: "127.0.0.1:18080",
    dialAddress: "10.244.0.8:8080",
    state: "running",
  });

  assert.deepEqual(details, [
    { local: "127.0.0.1:18080", remote: "10.244.0.8:8080", flow: "bidirectional" },
  ]);
  assert.deepEqual(trafficBindingDetails(binding), [
    { local: "—", remote: "10.244.1.200:80", flow: "bidirectional" },
  ]);
});

test("marks mirror traffic as cluster to local only", () => {
  const details = trafficBindingDetails({
    id: "task-1",
    name: "binding-1",
    namespace: "development",
    sessionId: "session-1",
    mode: "Mirror",
    desiredState: "Active",
    phase: "Ready",
    target: { kind: "Service", name: "api" },
    serviceClusterIp: "10.96.0.15",
    ports: [{
      targetPort: 80,
      localHost: "127.0.0.1",
      localPort: 8000,
      protocol: "TCP",
    }],
    createdAt: "2026-09-01T00:00:00Z",
  });

  assert.deepEqual(details, [{
    local: "127.0.0.1:8000",
    remote: "10.96.0.15:80",
    flow: "remote-to-local",
  }]);
});

test("omits the protocol suffix from session endpoints", () => {
  const details = trafficBindingDetails({
    id: "task-1",
    name: "binding-1",
    namespace: "development",
    sessionId: "session-1",
    mode: "Preview",
    desiredState: "Active",
    phase: "Ready",
    preview: { serviceName: "api-preview" },
    serviceClusterIp: "10.96.78.245",
    ports: [{
      targetPort: 7878,
      localHost: "127.0.0.1",
      localPort: 7878,
      protocol: "TCP",
    }],
    createdAt: "2026-09-01T00:00:00Z",
  });

  assert.deepEqual(details, [{
    local: "127.0.0.1:7878",
    remote: "10.96.78.245:7878",
    flow: "bidirectional",
  }]);
});
