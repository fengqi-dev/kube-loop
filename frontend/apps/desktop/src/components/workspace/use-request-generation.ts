import { useEffect, useRef } from "react";
import { createRequestGeneration } from "./request-generation";
export function useRequestGeneration() {
  const guard = useRef(createRequestGeneration());
  useEffect(() => () => guard.current.invalidate(), []);
  return guard.current;
}
