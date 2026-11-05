import { useState } from "react";
import { fetchJSON } from "../api.js";
import { routingOverview, serverSwitchRequest } from "../routingOverview.js";
import Icon from "./Icon.jsx";

export default function RoutingOverview({ config, statusMap, failoverMap, busy, onChanged, onConfigure, showToast, setPageError, t }) {
  const rows = routingOverview(config, statusMap, failoverMap);
  const [editing, setEditing] = useState("");
  const [location, setLocation] = useState("");
  const [saving, setSaving] = useState(false);
  const serverCount = new Set(rows.map(row => row.activeLocation).filter(Boolean)).size;

  async function changeServer(event, row) {
    event.preventDefault();
    setSaving(true);
    try {
      const request = serverSwitchRequest(row, location);
      await fetchJSON(request.path, { method: request.method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(request.body), timeoutMs: 150_000 });
      await onChanged();
      setEditing("");
      showToast(t("connections.locationUpdated"));
    } catch (error) {
      setPageError(error.message);
      showToast(error.message, true);
    } finally { setSaving(false); }
  }

  async function returnToAuto(row) {
    setSaving(true);
    try {
      await fetchJSON(`/api/priority-policies/${encodeURIComponent(row.policyId)}/override`, { method: "DELETE", timeoutMs: 150_000 });
      await onChanged();
      showToast(t("connections.priorityOverrideCleared"));
    } catch (error) {
      setPageError(error.message);
      showToast(error.message, true);
    } finally { setSaving(false); }
  }

  return (
    <section aria-labelledby="current-routing-title" className="overflow-hidden rounded-2xl border border-outline-variant/20 bg-surface-container-low">
      <div className="border-b border-outline-variant/20 p-5 sm:p-6">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 id="current-routing-title" className="font-headline text-xl font-semibold text-on-surface">{t("connections.currentScheme")}</h2>
          <span className="text-xs text-on-surface-variant">{t("connections.schemeSummary", { lists: rows.length, servers: serverCount })}</span>
        </div>
        <p className="mt-2 max-w-2xl text-sm text-on-surface-variant">{t("connections.currentSchemeHelp")}</p>
      </div>
      {rows.length === 0 ? <p className="p-6 text-sm text-on-surface-variant">{t("connections.noCurrentRoutes")}</p> : (
        <div className="divide-y divide-outline-variant/20">
          {rows.map(row => {
            const name = row.shared ? t("connections.sharedList") : row.name;
            return <article key={row.id} aria-label={name} className="p-5 sm:p-6">
              <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_auto] lg:items-center">
                <div className="min-w-0">
                  <h3 className="font-headline text-base font-semibold text-on-surface">{name}</h3>
                  <p className="mt-1 text-xs text-on-surface-variant">{t("connections.listEntryCount", { count: row.count })} · {row.providerName}</p>
                </div>
                <div className="min-w-0">
                  <div className="mb-1 flex items-center gap-2 text-xs text-on-surface-variant">
                    <span className={`h-1.5 w-1.5 rounded-full ${row.activeLocation ? "bg-secondary" : "bg-outline"}`} />
                    {t(row.unavailable ? "connections.noVpnServer" : row.failover ? "connections.failoverRouteActive" : row.policyId ? row.overrideLocation ? "connections.manualServer" : "connections.autoServer" : "connections.fixedServer")}
                  </div>
                  <p className="break-words text-sm font-medium text-on-surface">{row.unavailable ? t("connections.noVpnServer") : row.activeLocation || row.configuredLocation || t("connections.waitingServer")}</p>
                  {row.policyId && !row.activeLocation && <p className="mt-1 text-xs text-on-surface-variant">{t("connections.waitingServer")}</p>}
                </div>
                <div className="flex flex-wrap items-center gap-2 lg:justify-end">
                  <button type="button" disabled={busy || saving || row.targets.length < 1} onClick={() => { if (!row.canQuickSwitch) { onConfigure(row, "automation"); return; } setEditing(row.id); setLocation(row.activeLocation || row.configuredLocation || row.targets[0] || ""); }} className="inline-flex items-center gap-2 rounded-lg bg-primary/10 px-3 py-2 text-sm font-medium text-primary hover:bg-primary/20 disabled:opacity-40"><Icon name="swap_horiz" className="h-4 w-4" />{t(row.canQuickSwitch ? "connections.switchServer" : "connections.configureServers")}</button>
                  <button type="button" onClick={() => onConfigure(row, "domains")} className="rounded-lg px-3 py-2 text-sm text-on-surface-variant hover:bg-surface-container-high">{t("connections.configureRoute")}</button>
                </div>
              </div>
              <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs">
                {row.policyId && <button type="button" onClick={() => onConfigure(row, "automation")} className="text-primary hover:underline">{t("connections.fallbackOrder")}</button>}
                {row.overrideLocation && <button type="button" disabled={busy || saving} onClick={() => returnToAuto(row)} className="text-primary hover:underline disabled:opacity-40">{t("connections.returnToAuto")}</button>}
              </div>
              {editing === row.id && <form onSubmit={event => changeServer(event, row)} className="mt-4 rounded-lg bg-surface-container p-4">
                <label className="block text-sm font-medium text-on-surface" htmlFor={`server-choice-${row.id}`}>{t("connections.chooseServerFor", { name })}</label>
                <select id={`server-choice-${row.id}`} value={location} onChange={event => setLocation(event.target.value)} disabled={saving} className="mt-2 w-full min-w-0 rounded-lg border border-outline-variant/30 bg-surface-container-low px-3 py-2 text-sm text-on-surface">
                  <option value="" disabled>{t("connections.prioritySelectTarget")}</option>
                  {row.targets.map(target => <option key={target} value={target}>{target}</option>)}
                </select>
                <p className="mt-2 text-xs text-on-surface-variant">{t(row.policyId ? "connections.manualServerHelp" : "connections.fixedServerHelp")}</p>
                <div className="mt-4 flex gap-2">
                  <button type="submit" disabled={saving || !location} className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-on-primary disabled:opacity-40">{saving ? t("connections.changingLocation") : t("connections.applyLocation")}</button>
                  <button type="button" onClick={() => setEditing("")} disabled={saving} className="rounded-lg px-3 py-2 text-sm text-on-surface-variant hover:bg-surface-container-high">{t("connections.cancel")}</button>
                </div>
              </form>}
            </article>;
          })}
        </div>
      )}
    </section>
  );
}
