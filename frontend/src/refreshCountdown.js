export function refreshCountdown(state, now = Date.now()) {
  if (state?.phase !== "waiting" || !Number.isFinite(state.nextRunAt)) return { seconds: null, remaining: null };
  const interval = Math.max(1, state.intervalMs || 10_000);
  const remainingMs = Math.min(interval, Math.max(0, state.nextRunAt - now));
  return { seconds: Math.ceil(remainingMs / 1000), remaining: remainingMs / interval };
}
