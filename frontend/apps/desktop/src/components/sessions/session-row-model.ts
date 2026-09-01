import type {
  ServerPortForwardInfo,
  ServerTrafficBindingPort,
  ServerTrafficBindingSession,
} from "@/types";

export type TrafficEndpointPair = {
  local: string;
  remote: string;
  flow: "bidirectional" | "remote-to-local";
};

export type SessionTargetType = "Pod" | "Service" | "—";

export function sessionTargetType(
  item: ServerTrafficBindingSession,
  localPortForward?: ServerPortForwardInfo,
): SessionTargetType {
  if (item.mode !== "PortForward") return "Service";
  return targetTypeLabel(item.target?.kind ?? localPortForward?.kind);
}

export function targetTypeLabel(kind: string | undefined): SessionTargetType {
  if (kind?.toLocaleLowerCase() === "pod") return "Pod";
  if (kind?.toLocaleLowerCase() === "service") return "Service";
  return "—";
}

export function targetWithPorts(name: string, ports: number[]): string {
  const values = [...new Set(ports.filter((port) => Number.isInteger(port) && port > 0))];
  return `${name}:${values.length > 0 ? values.join(",") : "—"}`;
}

export function trafficBindingDetails(
  item: ServerTrafficBindingSession,
  localPortForward?: ServerPortForwardInfo,
): TrafficEndpointPair[] {
  return item.ports.flatMap((port) => item.mode === "PortForward"
    ? portForwardDetail(item, port, localPortForward)
    : reverseTrafficDetail(item, port));
}

function portForwardDetail(
  item: ServerTrafficBindingSession,
  port: ServerTrafficBindingPort,
  localPortForward?: ServerPortForwardInfo,
): TrafficEndpointPair[] {
  const localAddress = localPortForward?.address ?? localEndpoint(port) ?? "—";
  const targetAddress = localPortForward?.dialAddress ?? item.dialAddress ??
    remoteEndpoint(undefined, port.targetPort);
  return [{ local: localAddress, remote: targetAddress, flow: "bidirectional" }];
}

function reverseTrafficDetail(
  item: ServerTrafficBindingSession,
  port: ServerTrafficBindingPort,
): TrafficEndpointPair[] {
  return [{
    local: localEndpoint(port) ?? "—",
    remote: remoteEndpoint(
      item.serviceClusterIp,
      port.targetPort,
    ),
    flow: item.mode === "Mirror" ? "remote-to-local" : "bidirectional",
  }];
}

function remoteEndpoint(
  host: string | undefined,
  port: number,
) {
  return hostPort(host, port);
}

function localEndpoint(port: ServerTrafficBindingPort) {
  if (port.localPort === undefined) return port.localHost;
  return hostPort(port.localHost, port.localPort);
}

function hostPort(host: string | undefined, port: number) {
  if (!host) return String(port);
  const formattedHost = host.includes(":") && !host.startsWith("[") ? `[${host}]` : host;
  return `${formattedHost}:${port}`;
}
