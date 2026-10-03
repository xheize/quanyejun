export type ConnectionState = "connecting" | "online" | "offline";

// One shared budget per page mount, including repeated WebSocket disconnects.
export function connectRuntime(options: {
  apiBase: string;
  onBootstrap: (response: Response, signal: AbortSignal) => Promise<void>;
  cursor: () => string;
  onMessage: (data: string) => void;
  onState: (state: ConnectionState) => void;
  onExhausted: () => void;
}) {
  if (!options.apiBase) return () => {};

  let cancelled = false;
  let retries = 0;
  let socket: WebSocket | undefined;
  let retry: ReturnType<typeof setTimeout> | undefined;
  const controller = new AbortController();

  function scheduleRetry() {
    if (cancelled) return;
    options.onState("offline");
    if (retries >= 10) {
      options.onExhausted();
      return;
    }
    retries += 1;
    retry = setTimeout(hydrate, 2500);
  }

  async function hydrate() {
    if (cancelled) return;
    options.onState("connecting");
    try {
      const response = await fetch(`${options.apiBase}/api/bootstrap`, {
        credentials: "include", signal: controller.signal,
      });
      if (cancelled) return;
      await options.onBootstrap(response, controller.signal);
      if (cancelled) return;
      const wsBase = options.apiBase.replace(/^http/, "ws");
      socket = new WebSocket(`${wsBase}/ws?lastEventId=${encodeURIComponent(options.cursor())}`);
      const currentSocket = socket;
      socket.onopen = () => { if (!cancelled) options.onState("online"); };
      socket.onmessage = (message) => { if (!cancelled) options.onMessage(message.data); };
      socket.onclose = scheduleRetry;
      socket.onerror = () => currentSocket.close();
    } catch {
      scheduleRetry();
    }
  }

  void hydrate();
  return () => {
    cancelled = true;
    controller.abort();
    if (retry) clearTimeout(retry);
    socket?.close();
  };
}
