// Each mounted view owns its timer and request. Hidden views do no polling.
export function startVisiblePolling(run, { visibility = document, intervalMs = 10_000, setIntervalFn = setInterval, clearIntervalFn = clearInterval, nowFn = Date.now, onState = () => {} } = {}) {
  let stopped = false, timer, pending = null, nextRunAt = null;
  const report = phase => onState({ phase, nextRunAt, intervalMs });
  async function tick() {
    if (stopped || visibility.visibilityState !== "visible" || pending) return;
    const controller = new AbortController();
    pending = controller;
    report("running");
    try { await run(controller.signal); } catch { /* Retry on the next visible tick. */ }
    finally {
      if (pending === controller) pending = null;
      if (!stopped && visibility.visibilityState === "visible") report("waiting");
    }
  }
  function syncVisibility() {
    if (timer !== undefined) { clearIntervalFn(timer); timer = undefined; }
    if (stopped) return;
    if (visibility.visibilityState !== "visible") { nextRunAt = null;pending?.abort();report("paused");return; }
    nextRunAt = nowFn() + intervalMs;
    void tick();
    timer = setIntervalFn(() => { nextRunAt = nowFn() + intervalMs;return tick(); }, intervalMs);
  }
  visibility.addEventListener("visibilitychange", syncVisibility);
  syncVisibility();
  return () => {
    stopped = true;
    if (timer !== undefined) clearIntervalFn(timer);
    pending?.abort();
    visibility.removeEventListener("visibilitychange", syncVisibility);
  };
}
