import { useEffect, useRef, useCallback } from "react";
import { userManager } from "@/authProvider";
import { API_BASE_URL } from "@/config";

export interface SSEEvent {
  stream: string;
  data: unknown;
}

export function useSSE(onEvent: (event: SSEEvent) => void) {
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  const connect = useCallback(async (signal: AbortSignal) => {
    while (!signal.aborted) {
      try {
        const user = await userManager.getUser();
        if (!user?.access_token || signal.aborted) return;

        const resp = await fetch(`${API_BASE_URL}/events`, {
          headers: { Authorization: `Bearer ${user.access_token}` },
          signal,
        });

        if (!resp.ok || !resp.body) {
          // wait before retry
          await delay(500, signal);
          continue;
        }

        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";

        while (!signal.aborted) {
          const { done, value } = await reader.read();
          if (done) break;

          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split("\n");
          // keep the last (possibly incomplete) line in the buffer
          buffer = lines.pop() ?? "";

          for (const line of lines) {
            if (line.startsWith("data:")) {
              const raw = line.slice(5).trim();
              if (!raw || raw === "") continue;
              try {
                const parsed = JSON.parse(raw) as SSEEvent;
                onEventRef.current(parsed);
              } catch {
                // not valid JSON, skip
              }
            }
          }
        }

        reader.releaseLock();
      } catch (err: unknown) {
        if (signal.aborted) return;
        // network error or abort — retry after delay
      }

      await delay(500, signal);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    connect(controller.signal);
    return () => controller.abort();
  }, [connect]);
}

function delay(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener("abort", () => { clearTimeout(timer); resolve(); }, { once: true });
  });
}
