import type { TrafficInspectionEvent } from "@/types";

export interface TrafficInspectionExchange {
  flowId: string;
  events: TrafficInspectionEvent[];
  firstEvent: TrafficInspectionEvent;
  summaryEvent: TrafficInspectionEvent;
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
