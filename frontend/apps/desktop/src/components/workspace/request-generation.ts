// A new request or scope disposal invalidates every older response.
export function createRequestGeneration() {
  let generation = 0;
  return {
    begin() { const request = ++generation; return () => request === generation; },
    invalidate() { generation += 1; },
  };
}
