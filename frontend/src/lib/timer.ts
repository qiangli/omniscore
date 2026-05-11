// Server-authoritative timer math.
//
// On each /answer roundtrip the server returns (server_now_ms, deadline_at).
// We treat the server's clock as authoritative and store the offset between
// the two clocks: skew = server_now_ms - performance.now()-ish-anchored-ms.
// Display = max(0, deadline_at - (Date.now() + skew)).

export type TimerSync = {
  serverNowMs: number;
  deadlineAt: number;
  syncedAtClientMs: number;
};

export function newSync(serverNowMs: number, deadlineAt: number): TimerSync {
  return {
    serverNowMs,
    deadlineAt,
    syncedAtClientMs: Date.now(),
  };
}

export function remainingMs(sync: TimerSync, nowMs: number = Date.now()): number {
  const skew = sync.serverNowMs - sync.syncedAtClientMs;
  const adjustedNow = nowMs + skew;
  return Math.max(0, sync.deadlineAt - adjustedNow);
}

export function formatMMSS(ms: number): string {
  const totalSec = Math.floor(ms / 1000);
  const mm = Math.floor(totalSec / 60);
  const ss = totalSec % 60;
  return `${mm}:${ss.toString().padStart(2, "0")}`;
}
