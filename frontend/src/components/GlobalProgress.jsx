import { useEffect, useRef, useState } from "react";
import { subscribeLoading } from "../api.js";
import { useI18n } from "../i18n.jsx";
import ActivitySpinner from "./ActivitySpinner.jsx";

export default function GlobalProgress() {
  const { t } = useI18n();
  const [active, setActive] = useState(false);
  const [operations, setOperations] = useState([]);
  const [now, setNow] = useState(Date.now);
  const activeRef = useRef(false);

  useEffect(() => {
    // Delay showing the indicator so truly fast operations (< 250ms) stay silent.
    // Only longer saves / heavy actions will actually surface the spinner.
    let showTimer = null;
    const setVisible = (value) => {
      activeRef.current = value;
      setActive(value);
    };
    const unsub = subscribeLoading((count, pending) => {
      setOperations(pending || []);
      if (count > 0) {
        if (!showTimer && !activeRef.current) {
          showTimer = setTimeout(() => {
            showTimer = null;
            setVisible(true);
          }, 250);
        }
      } else {
        if (showTimer) { clearTimeout(showTimer); showTimer = null; }
        setVisible(false);
      }
    });
    return () => {
      unsub();
      if (showTimer) clearTimeout(showTimer);
    };
  }, []);

  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [active]);

  const current = operations[0];
  const elapsed = Math.max(0, Math.floor((now - (current?.startedAt ?? now)) / 1000));
  const elapsedLabel = `${String(Math.floor(elapsed / 60)).padStart(2, "0")}:${String(elapsed % 60).padStart(2, "0")}`;

  return (
    <>
      {/* Top progress bar */}
      <div
        className={`pointer-events-none fixed top-0 left-0 right-0 z-[100] h-0.5 overflow-hidden transition-opacity duration-200 ${
          active ? "opacity-100" : "opacity-0"
        }`}
      >
        <div className="operation-sweep h-full w-1/3 rounded-full bg-primary" />
      </div>

      {/* Floating spinner pill */}
      {active && (
        <div role="status" aria-live="polite" className="pointer-events-none fixed bottom-5 left-4 right-4 z-[100] mx-auto flex max-w-sm items-center gap-3 rounded-2xl border border-outline-variant/25 bg-surface-container-high px-4 py-3 text-on-surface shadow-xl sm:bottom-6">
          <ActivitySpinner className="h-10 w-10 shrink-0 text-primary" />
          <div className="min-w-0 flex-1">
            <p className="text-sm font-semibold">{t(current?.labelKey || "loading.processing")}</p>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-on-surface-variant">
              <span aria-hidden="true" className="font-mono tabular-nums">{t("loading.elapsed")} {elapsedLabel}</span>
              {operations.length > 1 && <span>{t("loading.additional", { count: operations.length - 1 })}</span>}
            </div>
          </div>
        </div>
      )}
    </>
  );
}
