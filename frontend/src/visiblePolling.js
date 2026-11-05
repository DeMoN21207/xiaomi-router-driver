// Each mounted view owns its timer and request. Hidden views do no polling.
export function startVisiblePolling(run, { visibility = document, intervalMs = 10_000, setIntervalFn = setInterval, clearIntervalFn = clearInterval } = {}) {
  let stopped = false, timer, pending = null;
  async function tick() {
    if (stopped || visibility.visibilityState !== "visible" || pending) return;
    const controller = new AbortController();
    pending = controller;
    try { await run(controller.signal); } catch { /* Retry on the next visible tick. */ }
    finally { if (pending === controller) pending = null; }
  }
  function syncVisibility() {
    if (timer !== undefined) { clearIntervalFn(timer); timer = undefined; }
    if (stopped) return;
    if (visibility.visibilityState !== "visible") { pending?.abort(); return; }
    void tick();
    timer = setIntervalFn(tick, intervalMs);
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
