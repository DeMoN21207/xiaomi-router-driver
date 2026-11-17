import { useEffect, useState } from "react";
import { fetchJSON } from "../api.js";
import { useI18n } from "../i18n.jsx";
import Icon from "../components/Icon.jsx";

export default function DevicesPage() {
  const { t } = useI18n();
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [query, setQuery] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    fetchJSON("/api/devices", { signal: controller.signal })
      .then(setData)
      .catch((err) => { if (!controller.signal.aborted) setError(err.message); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, []);

  async function refresh() {
    setLoading(true); setError("");
    try { setData(await fetchJSON("/api/devices")); }
    catch (err) { setError(err.message); }
    finally { setLoading(false); }
  }

  async function toggle(device) {
    if (busy || loading) return;
    setBusy(device.mac); setError(""); setNotice("");
    try {
      const result = await fetchJSON("/api/devices", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ mac: device.mac, name: device.name, excluded: !device.excluded }),
        timeoutMs: 75_000,
        trackLoading: false,
      });
      setData(result);
      setNotice(`${device.name || device.mac}: ${t(device.excluded ? "devices.restored" : "devices.excluded")}`);
    } catch (err) {
      setError(err.message);
      // Switching this computer can reconnect the HTTP request. Reconcile with
      // persisted state; never guess that a failed request changed the mode.
      try { setData(await fetchJSON("/api/devices")); } catch { /* keep the last confirmed list */ }
    } finally { setBusy(""); }
  }

  const devices = data?.devices || [];
  const search = query.trim().toLocaleLowerCase();
  const visible = devices.filter((d) => [d.name, d.mac, ...(d.ips || [])].join(" ").toLocaleLowerCase().includes(search));
  const excludedCount = devices.filter((d) => d.excluded).length;

  return (
    <div className="space-y-6 min-w-0">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <h1 className="font-headline text-2xl sm:text-3xl font-bold">{t("devices.title")}</h1>
          <p className="mt-2 max-w-2xl text-sm sm:text-base text-on-surface-variant">{t("devices.subtitle")}</p>
        </div>
        <button type="button" onClick={refresh} disabled={loading || Boolean(busy)} className="flex min-h-11 shrink-0 items-center justify-center gap-2 rounded-xl border border-outline-variant/30 px-4 py-2 text-sm text-on-surface-variant hover:text-primary disabled:opacity-50">
          <Icon name="refresh" className={`h-4 w-4 ${loading ? "animate-spin" : ""}`} />{t("devices.refresh")}
        </button>
      </div>

      <div className="rounded-2xl border border-outline-variant/20 bg-surface-container-low p-4 sm:p-5">
        <p className="font-semibold">{t("devices.default")}</p>
        <p className="mt-1 text-sm text-on-surface-variant">{t("devices.explanation")}</p>
      </div>
      {error && <div role="alert" className="break-words rounded-xl border border-error/30 bg-error/10 p-4 text-sm text-error">{error}</div>}
      <p role="status" className={notice ? "text-sm text-primary" : "sr-only"}>{notice}</p>

      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <label className="flex min-h-11 items-center gap-2 rounded-xl border border-outline-variant/30 bg-surface-container px-3 sm:w-80">
          <Icon name="search" className="h-4 w-4 shrink-0 text-on-surface-variant" />
          <span className="sr-only">{t("devices.search")}</span>
          <input type="search" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("devices.search")} className="w-full min-w-0 bg-transparent py-2 text-sm outline-none" />
        </label>
        {data && <p className="text-sm text-on-surface-variant">{t("devices.total")}: {devices.length} · {t("devices.exceptions")}: {excludedCount}</p>}
      </div>

      <div className="grid gap-3" aria-busy={loading || Boolean(busy)}>
        {!data && loading && <p className="p-5 text-on-surface-variant">{t("devices.loading")}</p>}
        {data && visible.length === 0 && <p className="rounded-2xl bg-surface-container-low p-6 text-on-surface-variant">{t(query ? "devices.noResults" : "devices.empty")}</p>}
        {visible.map((device) => (
          <article key={device.mac} className="flex min-w-0 flex-col gap-4 rounded-2xl border border-outline-variant/20 bg-surface-container-low p-4 sm:p-5 lg:flex-row lg:items-center" aria-label={device.name || device.mac}>
            <div className="flex min-w-0 flex-1 items-start gap-3">
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-surface-container-high text-on-surface-variant"><Icon name="devices" className="h-5 w-5" /></div>
              <div className="min-w-0">
                <h2 className="break-words font-semibold">{device.name || t("devices.unnamed")}</h2>
                <p className="mt-1 break-all font-mono text-xs text-on-surface-variant">{device.ips?.length ? device.ips.join(" · ") : t("devices.offline")}</p>
                <p className="mt-1 break-all font-mono text-xs text-on-surface-variant">{device.mac}</p>
              </div>
            </div>
            <div className="min-w-0 lg:w-56 lg:shrink-0">
              <p className={`text-sm font-medium ${device.excluded ? "text-primary" : "text-on-surface"}`}>{t(device.excluded ? "devices.direct" : "devices.routed")}</p>
              <p className="mt-1 text-xs text-on-surface-variant">{t(device.excluded ? "devices.directHint" : "devices.routedHint")}</p>
            </div>
            <button type="button" disabled={Boolean(busy) || loading} onClick={() => toggle(device)} aria-label={`${t(device.excluded ? "devices.restore" : "devices.exclude")}: ${device.name || device.mac}`} className="flex min-h-11 w-full shrink-0 items-center justify-center gap-2 rounded-xl border border-outline-variant/30 bg-surface-container-high px-4 py-3 text-sm font-semibold text-primary transition-colors hover:bg-primary/15 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary disabled:opacity-50 sm:w-auto lg:w-44">
              {busy === device.mac && <Icon name="sync" className="h-4 w-4 animate-spin" />}
              {t(busy === device.mac ? "devices.applying" : device.excluded ? "devices.restore" : "devices.exclude")}
            </button>
          </article>
        ))}
      </div>
      <p className="max-w-3xl text-xs leading-relaxed text-on-surface-variant">{t("devices.identityHint")}</p>
    </div>
  );
}
