export type RunStatus = { id: string; status: string };

// Events and reconnect snapshots can arrive before the POST response identifies
// our run. Retain terminal statuses until that response has been matched.
export function createPendingRun(onChange: (sending: boolean, failed?: boolean) => void) {
  let pending: { id?: string; terminal: Map<string, RunStatus> } | undefined;

  function observe(run: RunStatus) {
    if (!pending || (run.status !== "completed" && run.status !== "failed")) return;
    pending.terminal.set(run.id, run);
    if (pending.id === run.id) {
      pending = undefined;
      onChange(false, run.status === "failed");
    }
  }

  return {
    begin() {
      const request = { terminal: new Map<string, RunStatus>() };
      pending = request;
      onChange(true);
      return request;
    },
    accept(request: object, run: RunStatus) {
      if (!pending || pending !== request) return false;
      pending.id = run.id;
      observe(pending.terminal.get(run.id) || run);
      return true;
    },
    observe,
    async restore(runs: RunStatus[], load: (id: string) => Promise<RunStatus | null>) {
      for (const run of runs) observe(run);
      const request = pending;
      // Bootstrap only includes the eight most recently updated runs.
      if (!request?.id || runs.some(run => run.id === request.id)) return;
      const run = await load(request.id);
      if (pending !== request) return;
      if (run) observe(run);
      else {
        pending = undefined;
        onChange(false);
      }
    },
    reset(request: object | undefined = pending) {
      if (request !== pending) return false;
      pending = undefined;
      onChange(false);
      return true;
    },
  };
}
