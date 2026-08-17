import { useEffect, useRef, useState } from "react";
import type { Metrics } from "@/types";

export type TrafficPoint = {
  at: number;
  upload: number;
  download: number;
};

const windowMs = 10 * 60 * 1000;

export function useTrafficHistory(
  ready: boolean,
  metrics: Metrics | undefined,
) {
  const [history, setHistory] = useState<TrafficPoint[]>([]);
  const [uploadSpeed, setUploadSpeed] = useState(0);
  const [downloadSpeed, setDownloadSpeed] = useState(0);
  const previous = useRef<{
    at: number;
    uploadTotal: number;
    downloadTotal: number;
  } | null>(null);

  useEffect(() => {
    if (!ready) {
      previous.current = null;
      setHistory([]);
      setUploadSpeed(0);
      setDownloadSpeed(0);
      return;
    }

    const uploadTotal = metrics?.uploadTotal ?? 0;
    const downloadTotal = metrics?.downloadTotal ?? 0;
    const at = Date.now();
    const last = previous.current;

    let nextUpload = 0;
    let nextDownload = 0;
    if (last && at > last.at) {
      const elapsed = (at - last.at) / 1000;
      if (elapsed > 0 && elapsed < 10) {
        nextUpload = Math.max(0, (uploadTotal - last.uploadTotal) / elapsed);
        nextDownload = Math.max(
          0,
          (downloadTotal - last.downloadTotal) / elapsed,
        );
      }
    }

    previous.current = { at, uploadTotal, downloadTotal };
    setUploadSpeed(nextUpload);
    setDownloadSpeed(nextDownload);
    setHistory((current) => {
      const next = [
        ...current,
        { at, upload: nextUpload, download: nextDownload },
      ];
      const cutoff = at - windowMs;
      let start = 0;
      while (start < next.length && next[start].at < cutoff) start += 1;
      return start === 0 ? next : next.slice(start);
    });
  }, [ready, metrics]);

  return {
    history,
    uploadSpeed,
    downloadSpeed,
    uploadTotal: metrics?.uploadTotal ?? 0,
    downloadTotal: metrics?.downloadTotal ?? 0,
    connections:
      metrics?.activeConnections ??
      (Array.isArray(metrics?.connections) ? metrics.connections.length : 0),
    memory: metrics?.memory ?? 0,
    windowMs,
  };
}
