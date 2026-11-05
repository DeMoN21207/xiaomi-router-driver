import { useEffect, useState } from "react";
import { refreshCountdown } from "../refreshCountdown.js";
import ActivitySpinner from "./ActivitySpinner.jsx";

export default function RefreshCountdown({ state, loading = false, failed = false, checkedAt, t }) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    setNow(Date.now());
    if (state.phase !== "waiting" || loading) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [state.phase, state.nextRunAt, loading]);
  const running = loading || state.phase === "running";
  const { seconds, remaining } = refreshCountdown(state, now);
  const label = loading ? t("loading.catalogue") : running ? t("loading.measuringPing") : state.phase === "paused" ? t("loading.paused") : t(failed ? "loading.retryCountdown" : "loading.refreshCountdown", { seconds: seconds ?? 0 });
  return <div role="timer" aria-live="off" aria-label={label} className="mt-4 flex min-w-0 items-center gap-3">
    <div aria-hidden="true" className="relative flex h-11 w-11 shrink-0 items-center justify-center text-primary">
      {running ? <ActivitySpinner className="h-11 w-11" /> : <>
        <svg className="absolute inset-0 h-11 w-11 -rotate-90" viewBox="0 0 44 44" fill="none">
          <circle cx="22" cy="22" r="18" stroke="currentColor" strokeWidth="2" opacity="0.16" />
          <circle className="refresh-countdown-ring" cx="22" cy="22" r="18" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" pathLength="1" strokeDasharray="1" strokeDashoffset={1 - (remaining ?? 0)} />
        </svg>
        <span className="font-mono text-sm font-semibold tabular-nums">{seconds ?? "–"}</span>
      </>}
    </div>
    <div className="min-w-0">
      <p className="text-sm font-medium text-on-surface">{label}</p>
      <p className="mt-0.5 text-xs text-on-surface-variant">{t("loading.visibleOnly")}{checkedAt && <> · {t("loading.lastUpdate")} <time dateTime={checkedAt}>{new Date(checkedAt).toLocaleTimeString()}</time></>}</p>
    </div>
  </div>;
}
