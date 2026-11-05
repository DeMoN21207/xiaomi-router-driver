import { useEffect, useState, useMemo, useCallback, useRef } from "react";
import { fetchJSON } from "../api.js";
import { eventMessage } from "../eventMessage.js";
import { useI18n } from "../i18n.jsx";
import Icon from "../components/Icon.jsx";
import InlineNotice from "../components/InlineNotice.jsx";
import { formatDateFull, levelBadge, levelIcon, levelIconColor } from "../utils.js";

const PAGE_SIZE = 25;

export default function EventsPage() {
  const { t } = useI18n();
  const [events, setEvents] = useState([]);
  const [total, setTotal] = useState(0);
  const [levelCounts, setLevelCounts] = useState({ info: 0, warn: 0, error: 0 });
  const [filter, setFilter] = useState("");
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(1);
  const [totalPages, setTotalPages] = useState(1);
  const [clearing, setClearing] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);
  const [error, setError] = useState("");
  const requestRef = useRef(null);
  const snapshotRef = useRef("");
  const listRef = useRef(null);
  const clearingRef = useRef(false);
  const datasetFilterRef = useRef("");

  const levelFilters = useMemo(
    () => [
      { value: "", label: t("events.all"), icon: "filter_list", color: "primary" },
      { value: "info", label: t("events.info"), icon: "info", color: "secondary" },
      { value: "warn", label: t("events.warnings"), icon: "warning", color: "warning" },
      { value: "error", label: t("events.errors"), icon: "error", color: "error" },
    ],
    [t],
  );

  const loadPage = useCallback(async (number = 1, resetSnapshot = false) => {
    if (clearingRef.current) return;
    requestRef.current?.abort();
    const controller = new AbortController();
    requestRef.current = controller;
    if (datasetFilterRef.current !== filter) {
      datasetFilterRef.current = filter;
      setEvents([]);
      setTotal(0);
      setPage(1);
      setTotalPages(1);
      snapshotRef.current = "";
    }
    setLoading(true);
    setConfirmClear(false);
    try {
      const params = new URLSearchParams({ limit: String(PAGE_SIZE), pagination: "numbered", page: String(number) });
      if (filter) params.set("level", filter);
      if (!resetSnapshot && snapshotRef.current) params.set("snapshot", snapshotRef.current);
      const data = await fetchJSON(`/api/events?${params}`, { signal: controller.signal });
      if (controller.signal.aborted) return;
      setEvents(data.events || []);
      setTotal(data.total || 0);
      setLevelCounts(data.levelCounts || { info: 0, warn: 0, error: 0 });
      setError("");
      setPage(data.page);
      setTotalPages(data.totalPages);
      snapshotRef.current = data.snapshot;
    } catch (err) {
      if (!controller.signal.aborted) setError(err.message);
    } finally {
      if (!controller.signal.aborted && requestRef.current === controller) { setLoading(false); requestRef.current = null; }
    }
  }, [filter]);

  useEffect(() => {
    void loadPage(1, true);
    return () => requestRef.current?.abort();
  }, [loadPage]);

  function goToPage(number) {
    void loadPage(number);
    listRef.current?.scrollIntoView({ block: "start" });
  }

  async function clearAll() {
    if (clearingRef.current) return;
    clearingRef.current = true;
    requestRef.current?.abort();
    requestRef.current = null;
    setClearing(true);
    setLoading(false);
    try {
      await fetchJSON("/api/events", { method: "DELETE" });
      setEvents([]);
      setTotal(0);
      setLevelCounts({ info: 0, warn: 0, error: 0 });
      setError("");
      setPage(1);
      setTotalPages(1);
      snapshotRef.current = "";
    } catch (err) {
      setError(err.message);
    } finally {
      setClearing(false);
      clearingRef.current = false;
    }
  }

  return (
    <div className="space-y-8">
      <div className="flex flex-col justify-between gap-6 md:flex-row md:items-end">
        <div>
          <h1 className="mb-2 font-headline text-4xl font-black tracking-tight">{t("events.title")}</h1>
          <div className="flex items-center gap-2">
            <span className="relative flex h-2 w-2">
              <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-secondary opacity-75" />
              <span className="status-glow-success relative inline-flex h-2 w-2 rounded-full bg-secondary" />
            </span>
            <p className="font-mono text-xs uppercase tracking-widest text-on-surface-variant">
              {loading ? t("events.loading") : `${total} ${t("events.entries")}`}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {events.length > 0 && (
            confirmClear ? (
              <div className="flex items-center gap-2">
                <span className="text-xs text-on-surface-variant">{t("events.clearConfirm")}</span>
                <button
                  type="button"
                  onClick={() => { setConfirmClear(false); void clearAll(); }}
                  disabled={clearing}
                  className="rounded-lg border border-error/30 bg-error/10 px-3 py-2 text-xs font-bold uppercase tracking-wider text-error transition-colors hover:bg-error/20 disabled:opacity-50"
                >
                  {t("events.clearYes")}
                </button>
                <button
                  type="button"
                  onClick={() => setConfirmClear(false)}
                  className="rounded-lg border border-outline-variant/20 px-3 py-2 text-xs font-bold uppercase tracking-wider text-on-surface-variant transition-colors hover:bg-surface-container"
                >
                  {t("connections.cancel")}
                </button>
              </div>
            ) : (
              <button
                type="button"
                onClick={() => setConfirmClear(true)}
                disabled={clearing}
                className="flex items-center gap-2 rounded-lg border border-error/20 px-4 py-2 text-xs font-bold uppercase tracking-wider text-error transition-colors duration-100 hover:bg-error/10 active:scale-95 disabled:opacity-50"
              >
                <Icon name="delete_sweep" className="h-4 w-4" />
                {t("events.clear")}
              </button>
            )
          )}
          <button
            type="button"
            onClick={() => void loadPage(1, true)}
            disabled={loading || clearing}
            className="flex items-center gap-2 rounded-lg border border-primary/20 px-4 py-2 text-xs font-bold uppercase tracking-wider text-primary transition-colors duration-100 hover:bg-primary/5 hover:text-on-surface active:scale-95"
          >
            <Icon name="refresh" className="h-4 w-4" />
            {t("events.refresh")}
          </button>
        </div>
      </div>

      {error ? <InlineNotice tone="error" title={t("error.events")} message={error} /> : null}

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        {levelFilters.map((item) => {
          const isActive = filter === item.value;
          const colorMap = {
            primary: { active: "border-primary/30 bg-primary/10", icon: "text-primary", text: "text-primary" },
            secondary: { active: "border-secondary/30 bg-secondary/10", icon: "text-secondary", text: "text-secondary" },
            warning: { active: "border-tertiary/30 bg-tertiary/10", icon: "text-tertiary", text: "text-tertiary" },
            error: { active: "border-error/30 bg-error/10", icon: "text-error", text: "text-error" },
          };
          const c = colorMap[item.color];
          const count = item.value === "" ? (levelCounts.info ?? 0) + (levelCounts.warn ?? 0) + (levelCounts.error ?? 0) : levelCounts[item.value] ?? 0;
          return (
            <button
              key={item.value}
              type="button"
              onClick={() => setFilter(item.value)}
              disabled={clearing}
              className={`rounded-xl p-4 text-left transition-colors ${
                isActive ? `border ${c.active}` : "border border-transparent bg-surface-container-low hover:bg-surface-container"
              }`}
            >
              <div className="mb-2 flex items-center gap-2">
                <Icon name={item.icon} className={`h-5 w-5 ${isActive ? c.icon : "text-outline"}`} />
                <span className="font-headline text-[10px] uppercase tracking-widest text-outline">{t("events.filter")}</span>
              </div>
              <div className="flex items-baseline gap-2">
                <span className={`font-headline font-bold ${isActive ? c.text : "text-on-surface"}`}>{item.label}</span>
                {!loading && <span className={`font-mono text-xs ${isActive ? c.text : "text-outline"}`}>{count}</span>}
              </div>
            </button>
          );
        })}
      </div>

      {total > 0 && <EventPagination page={page} totalPages={totalPages} total={total} count={events.length} disabled={loading || clearing} onPageChange={goToPage} t={t} />}

      <div ref={listRef} aria-busy={loading} className="scroll-mt-24 space-y-3">
        {loading ? <p className="p-6 text-sm text-on-surface-variant">{t("events.loading")}</p> : events.length === 0 ? (
          <div className="rounded-xl bg-surface-container-low p-12 text-center">
            <Icon name="history" className="mx-auto mb-3 h-12 w-12 text-outline-variant" />
            <p className="text-on-surface-variant">{error ? t("error.events") : filter ? t("events.emptyFiltered") : t("events.empty")}</p>
          </div>
        ) : (
          events.map((event) => {
            const help = eventHelpText(event.kind, t);
            return (
              <div key={event.id} className="rounded-xl border border-outline-variant/10 bg-surface-container-low p-5 transition-colors hover:bg-surface-container">
                <div className="mb-2 flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
                  <div className="flex flex-wrap items-center gap-3">
                    <Icon name={levelIcon(event.level)} className={`h-5 w-5 ${levelIconColor(event.level)}`} />
                    <span className={`rounded-full px-2.5 py-1 text-[10px] font-bold uppercase tracking-widest ${levelBadge(event.level)}`}>
                      {levelLabel(event.level, t)}
                    </span>
                    <span className="font-headline font-bold text-on-surface">{eventKindLabel(event.kind, t)}</span>
                    {help ? <EventHelp text={help} label={t("events.help")} /> : null}
                  </div>
                  <span className="font-mono text-xs text-outline">{formatDateFull(event.occurredAt)}</span>
                </div>
                <p className="whitespace-pre-wrap break-words text-sm text-on-surface-variant sm:ml-9 [overflow-wrap:anywhere]">{eventMessage(event.message)}</p>
              </div>
            );
          })
        )}

      </div>
      {total > PAGE_SIZE && <EventPagination page={page} totalPages={totalPages} total={total} count={events.length} disabled={loading || clearing} onPageChange={goToPage} t={t} />}
    </div>
  );
}

function EventPagination({ page, totalPages, total, count, disabled, onPageChange, t }) {
  const visible = new Set([1, totalPages, page - 1, page, page + 1]);
  if (page <= 3) [2, 3, 4].forEach(number => visible.add(number));
  if (page >= totalPages - 2) [totalPages - 3, totalPages - 2, totalPages - 1].forEach(number => visible.add(number));
  const pages = [...visible].filter(number => number >= 1 && number <= totalPages).sort((a, b) => a - b);
  const items = [];
  for (const number of pages) {
    const previous = items.at(-1);
    if (typeof previous === "number" && number - previous > 1) items.push(`gap-${number}`);
    items.push(number);
  }
  const buttonClass = "inline-flex h-10 min-w-10 items-center justify-center rounded-lg border px-2 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary disabled:cursor-default disabled:opacity-40";
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center sm:justify-between">
      <p aria-live="polite" className="text-sm text-on-surface-variant">{t("events.pageStatus", { from: (page - 1) * PAGE_SIZE + 1, to: (page - 1) * PAGE_SIZE + count, total })}</p>
      <nav aria-label={t("events.pagination")} className="flex flex-wrap items-center gap-1">
        <button type="button" aria-label={t("events.previousPage")} title={t("events.previousPage")} disabled={disabled || page <= 1} onClick={() => onPageChange(page - 1)} className={`${buttonClass} border-outline-variant/20 text-on-surface-variant hover:bg-surface-container`}><Icon name="chevron_left" className="h-4 w-4" /></button>
        {items.map(item => typeof item === "number" ? (
          <button key={item} type="button" aria-label={t("events.goToPage", { page: item })} aria-current={item === page ? "page" : undefined} disabled={disabled || item === page} onClick={() => onPageChange(item)} className={`${buttonClass} ${item === page ? "border-primary/30 bg-primary/15 text-primary disabled:opacity-100" : "border-outline-variant/20 text-on-surface-variant hover:bg-surface-container"}`}>{item}</button>
        ) : <span key={item} aria-hidden="true" className="px-1 text-outline">…</span>)}
        <button type="button" aria-label={t("events.nextPage")} title={t("events.nextPage")} disabled={disabled || page >= totalPages} onClick={() => onPageChange(page + 1)} className={`${buttonClass} border-outline-variant/20 text-on-surface-variant hover:bg-surface-container`}><Icon name="chevron_right" className="h-4 w-4" /></button>
      </nav>
    </div>
  );
}

function eventKindLabel(kind, t) {
  const key = `kind.${kind}`;
  const label = t(key);
  return label === key ? kind : label;
}

function eventHelpText(kind, t) {
  const key = `help.${kind}`;
  const help = t(key);
  return help === key ? "" : help;
}

function EventHelp({ text, label }) {
  return (
    <span className="group relative inline-flex">
      <button
        type="button"
        title={text}
        aria-label={label}
        className="inline-flex h-6 w-6 items-center justify-center rounded-full border border-outline-variant/20 bg-surface-container-highest text-outline transition-colors hover:border-primary/30 hover:text-primary"
      >
        <Icon name="help_outline" className="h-4 w-4" />
      </button>
      <span className="pointer-events-none absolute left-1/2 top-full z-40 mt-2 hidden w-72 -translate-x-1/2 rounded-lg border border-outline-variant/20 bg-surface-container-highest px-3 py-2 text-xs font-normal leading-relaxed text-on-surface shadow-xl group-hover:block">
        {text}
      </span>
    </span>
  );
}

function levelLabel(level, t) {
  switch (level) {
    case "info":
      return t("events.levelInfo");
    case "warn":
      return t("events.levelWarn");
    case "error":
      return t("events.levelError");
    default:
      return t("events.levelDefault");
  }
}
