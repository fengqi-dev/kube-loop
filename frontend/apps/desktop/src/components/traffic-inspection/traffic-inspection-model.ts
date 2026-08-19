import type { TrafficInspectionEvent } from "@/types";

export interface TrafficInspectionExchange {
  flowId: string;
  events: TrafficInspectionEvent[];
  firstEvent: TrafficInspectionEvent;
  summaryEvent: TrafficInspectionEvent;
}

const curlSkippedHeaders = new Set(["connection", "content-length", "host", "transfer-encoding"]);
const grpcurlSkippedHeaders = new Set([
  "connection",
  "content-length",
  "content-type",
  "grpc-accept-encoding",
  "grpc-encoding",
  "host",
  "te",
  "transfer-encoding",
  "user-agent",
]);

export function buildTrafficInspectionCommand(events: TrafficInspectionEvent[]): string | undefined {
  const request = events.find((event) => event.type === "request");
  if (!request) return undefined;
  if (request.protocol === "http" || request.protocol === "https") {
    return buildCurlCommand(request, findRequestBody(events));
  }
  return buildGrpcurlCommand(request, findRequestProtobuf(events));
}

export function groupTrafficInspectionEvents(
  events: TrafficInspectionEvent[],
): TrafficInspectionExchange[] {
  const grouped = new Map<string, TrafficInspectionEvent[]>();

  for (const event of events) {
    const flowId = event.flow_id || event.event_id;
    const flowEvents = grouped.get(flowId);
    if (flowEvents) {
      flowEvents.push(event);
    } else {
      grouped.set(flowId, [event]);
    }
  }

  return Array.from(grouped, ([flowId, flowEvents]) => {
    const orderedEvents = [...flowEvents].sort(compareEventTime);
    return {
      flowId,
      events: orderedEvents,
      firstEvent: orderedEvents[0],
      summaryEvent:
        findLastResponse(orderedEvents) ??
        orderedEvents.find((event) => event.type === "request") ??
        orderedEvents[orderedEvents.length - 1],
    };
  }).sort((left, right) => compareEventTime(right.firstEvent, left.firstEvent));
}

function findLastResponse(events: TrafficInspectionEvent[]) {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    if (events[index].type === "response") return events[index];
  }
  return undefined;
}

function compareEventTime(left: TrafficInspectionEvent, right: TrafficInspectionEvent) {
  const difference = Date.parse(left.timestamp) - Date.parse(right.timestamp);
  if (Number.isNaN(difference) || difference === 0) {
    return left.event_id.localeCompare(right.event_id);
  }
  return difference;
}

function buildCurlCommand(request: TrafficInspectionEvent, body?: string) {
  const host = request.http?.host || request.destination;
  if (!host) return undefined;
  const method = request.http?.method || "GET";
  const path = request.http?.path || "/";
  const command = ["curl", "-X", shellQuote(method)];
  appendHeaders(command, request.http?.request_headers, curlSkippedHeaders, "-H");
  if (body !== undefined && body.length > 0) {
    command.push("--data-binary", shellQuote(body));
  }
  command.push(shellQuote(`${request.protocol}://${host}${path.startsWith("/") ? path : `/${path}`}`));
  return command.join(" ");
}

function buildGrpcurlCommand(request: TrafficInspectionEvent, protobuf?: string) {
  const method = request.grpc?.path?.replace(/^\/+/, "") ||
    [request.grpc?.service, request.grpc?.method].filter(Boolean).join("/");
  const target = grpcTarget(request.http?.host || request.destination, request.tls);
  if (!method || !target) return undefined;
  const command = ["grpcurl"];
  if (!request.tls) command.push("-plaintext");
  appendHeaders(command, request.http?.request_headers, grpcurlSkippedHeaders, "-H");
  if (protobuf) command.push("-d", shellQuote(protobuf));
  command.push(shellQuote(target), shellQuote(method));
  return command.join(" ");
}

function appendHeaders(
  command: string[],
  headers: Record<string, string[]> | undefined,
  skipped: Set<string>,
  flag: string,
) {
  if (!headers) return;
  for (const name of Object.keys(headers).sort((left, right) => left.localeCompare(right))) {
    if (skipped.has(name.toLowerCase())) continue;
    for (const value of headers[name] ?? []) {
      command.push(flag, shellQuote(`${name}: ${value}`));
    }
  }
}

function findRequestBody(events: TrafficInspectionEvent[]) {
  const body = findLastRequestBodyEvent(events);
  if (!body?.raw || body.raw.format !== "http") return undefined;
  const raw = decodeRawText(body.raw.encoding, body.raw.data);
  if (raw === undefined) return undefined;
  const separator = raw.indexOf("\r\n\r\n");
  if (separator >= 0) return raw.slice(separator + 4);
  const fallback = raw.indexOf("\n\n");
  return fallback >= 0 ? raw.slice(fallback + 2) : undefined;
}

function findRequestProtobuf(events: TrafficInspectionEvent[]) {
  const protobuf = findLastRequestBodyEvent(events)?.protobuf;
  if (protobuf?.schema !== "proto" || !protobuf.data || protobuf.error) return undefined;
  try {
    const messages: unknown = JSON.parse(protobuf.data);
    if (!Array.isArray(messages) || messages.length === 0) return undefined;
    return messages.map((message) => JSON.stringify(message)).join("\n");
  } catch {
    return undefined;
  }
}

function findLastRequestBodyEvent(events: TrafficInspectionEvent[]) {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index];
    if (event.type === "body" && event.raw?.direction === "request") return event;
  }
  return undefined;
}

function decodeRawText(encoding: string, data: string) {
  if (encoding !== "base64") return data;
  try {
    const binary = globalThis.atob(data);
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    return undefined;
  }
}

function grpcTarget(authority: string | undefined, tls: boolean) {
  const target = authority?.trim();
  if (!target) return undefined;
  if (/^\[[^\]]+\]:\d+$/u.test(target) || /^[^:]+:\d+$/u.test(target)) return target;
  if (target.startsWith("[") && target.endsWith("]")) return `${target}:${tls ? 443 : 80}`;
  if (target.includes(":")) return `[${target}]:${tls ? 443 : 80}`;
  return `${target}:${tls ? 443 : 80}`;
}

function shellQuote(value: string) {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}
