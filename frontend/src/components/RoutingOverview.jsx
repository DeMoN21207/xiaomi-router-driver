import { useEffect, useRef, useState } from "react";
import { fetchJSON } from "../api.js";
import { routingOverview, serverSwitchRequest, prioritySelectionMessage, prioritySelectionError, routeDomainSources, domainSaveRequest, formatOutageDuration } from "../routingOverview.js";
import { parseDomainInput } from "../domainInput.js";
import { useI18n } from "../i18n.jsx";
import Icon from "./Icon.jsx";
import ServerPing from "./ServerPing.jsx";
import { formatPriorityPing } from "../priorityPing.js";

const secondaryButton = "inline-flex min-h-11 items-center justify-center gap-2 rounded-lg px-3 py-2 text-sm text-on-surface-variant hover:bg-surface-container-high disabled:opacity-40";
const primaryButton = "inline-flex min-h-11 items-center justify-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-on-primary disabled:opacity-40";

export default function RoutingOverview({ config, statusMap, failoverMap, busy, setBusy, onChanged, onConfigure, onEditingChange, showToast, setPageError, t }) {
  const rows = routingOverview(config, statusMap, failoverMap);
  const [editing, setEditing] = useState("");
  const [pending, setPending] = useState("");
  const [error, setError] = useState(null);
  const lock = useRef(false);

  function edit(id) {
    setEditing(id);
    setError(null);
    onEditingChange(Boolean(id));
  }

  async function apply(id, request, message) {
    if (lock.current || busy) return false;
    lock.current = true;
    setPending(id);
    setBusy(true);
    setError(null);
    setPageError("");
    try {
      const result = await fetchJSON(request.path, {
        method: request.method, timeoutMs: 150_000,
        ...(request.body ? { headers: { "Content-Type": "application/json" }, body: JSON.stringify(request.body) } : {}),
      });
      const feedback = typeof message === "function" ? message(result) : message;
      await onChanged();
      edit("");
      showToast(feedback);
      return true;
    } catch (failure) {
      setError({ id, message: prioritySelectionError(failure, t) });
      if (failure.code?.startsWith("priority_")) await onChanged();
      return false;
    } finally {
      lock.current = false;
      setPending("");
      setBusy(false);
    }
  }

  return <section aria-label={t("connections.sectionActive")} className="space-y-4">
    {rows.length === 0 && <div className="rounded-xl border border-outline-variant/20 p-5 text-sm text-on-surface-variant">{t("connections.noCurrentRoutes")}</div>}
    {rows.map(row => {
      const name = row.shared ? t("connections.sharedList") : row.name;
      const sources = routeDomainSources(config, row);
      return <article key={row.id} aria-label={name} className="min-w-0 rounded-xl border border-outline-variant/20 bg-surface-container-low p-4 sm:p-5">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0 flex-1">
            <h2 className="break-words font-headline text-lg font-semibold text-on-surface">{name}</h2>
            <p className="mt-1 text-xs text-on-surface-variant">{row.providerName}</p>
          </div>
          <button type="button" disabled={busy || Boolean(editing)} aria-label={t("connections.listSettingsFor", { name })} title={t("connections.listSettings")} onClick={() => onConfigure(row, "automation")} className={`${secondaryButton} -mr-2 shrink-0 text-xs`}><Icon name="settings" className="h-4 w-4" /><span className="hidden sm:inline">{t("connections.listSettings")}</span></button>
        </div>
        <div className="mt-4 grid min-w-0 gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)] lg:gap-8">
          <ServerControl row={row} name={name} editing={editing} edit={edit} busy={busy} pending={pending} error={error} apply={apply} onConfigure={onConfigure} t={t} />
          <div className="min-w-0 space-y-5 lg:border-l lg:border-outline-variant/20 lg:pl-8">
            {sources.map(source => <SiteList key={source.id} source={source} config={config} name={name} multiple={sources.length > 1} rowId={row.id} editing={editing} edit={edit} busy={busy} pending={pending} error={error} apply={apply} t={t} />)}
          </div>
        </div>
      </article>;
    })}
  </section>;
}

function ServerControl({ row, name, editing, edit, busy, pending, error, apply, onConfigure, t }) {
  const id = `${row.id}:server`;
  const [draft, setDraft] = useState(null);
  const selectRef = useRef(null);
  const selected = row.policyId ? row.overrideLocation || "" : row.configuredLocation;
  const choice = editing === id && draft !== null ? draft : selected;
  const changed = choice !== selected;
  const disabled = busy || Boolean(editing && editing !== id);
  const targets = [...new Set([...row.targets, ...(selected ? [selected] : [])])];
  const activeTarget = !row.failover && row.targetStatuses.find(item => item.location === row.activeLocation);
  const configuredOnly = !row.policyId && !row.failover && !row.activeLocation;
  const mode = row.unavailable ? "connections.noVpnServer" : row.failover ? "connections.failoverRouteActive" : row.policyId ? row.overrideLocation ? "connections.manualServer" : row.scheduledLocation ? "connections.scheduledServer" : "connections.automaticChoice" : "connections.manualServer";

  return <div className="min-w-0">
    <p className="text-xs text-on-surface-variant">{t(configuredOnly ? "connections.configuredServer" : "connections.workingServer")}</p>
    <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1">
      <p className="min-w-0 break-words text-base font-semibold text-on-surface">{row.unavailable || row.blocked ? t("connections.noVpnServer") : row.activeLocation || (configuredOnly && row.configuredLocation) || t("connections.waitingServer")}</p>
      {row.activeLocation && <ServerPing target={activeTarget} t={t} />}
    </div>
    {row.blocked || row.locationIssue
      ? <p className="mt-1 break-words text-sm text-error">{t(row.blocked ? "connections.routeBlocked" : row.activeLocation ? "connections.routeRetained" : "connections.routeUnconfirmed")}</p>
      : <p className="mt-1 text-xs text-on-surface-variant">{t(mode)}</p>}
    {row.policyId && <div className="mt-3 space-y-1 text-xs text-on-surface-variant">
      {row.currentOutage && <OutageMeasurement outage={row.currentOutage} ongoing />}
      {row.lastOutage && <OutageMeasurement outage={row.lastOutage} />}
      {!row.currentOutage && !row.lastOutage && <p>{t("connections.outageNoData")}</p>}
    </div>}
    {row.canQuickSwitch ? <form className="mt-4" onSubmit={async event => {
      event.preventDefault();
      if (!changed) return;
      if (await apply(id, serverSwitchRequest(row, choice), row.policyId ? result => prioritySelectionMessage(result, t) : t("connections.locationUpdated"))) setDraft(null);
    }}>
      <label htmlFor={`choice-${row.id}`} className="mb-2 block text-sm text-on-surface">{t("connections.chooseServer")}</label>
      <select ref={selectRef} id={`choice-${row.id}`} aria-label={t("connections.chooseServerFor", { name })} value={choice} disabled={disabled} onChange={event => {
        setDraft(event.target.value);
        edit(event.target.value === selected ? "" : id);
      }} className="min-h-11 w-full min-w-0 max-w-full rounded-lg border border-outline-variant/40 bg-surface-container px-3 py-2 text-sm text-on-surface">
        {row.policyId ? <option value="">{t("connections.automaticChoice")}</option> : <option value="" disabled>{t("connections.prioritySelectTarget")}</option>}
        {targets.map(target => <option key={target} value={target}>{target} · {formatPriorityPing(row.targetStatuses.find(item => item.location === target), t)}</option>)}
      </select>
      {row.policyId && (choice || row.overrideLocation) && <p className="mt-2 text-xs text-on-surface-variant">{t("connections.reserveStillWorks")}</p>}
      {editing === id && <div className="mt-3 flex flex-wrap gap-2">
        <button type="submit" disabled={disabled || !changed} className={primaryButton}>{pending === id ? t("connections.switchingServer") : t(choice ? "connections.switchNow" : "connections.enableAuto")}</button>
        <button type="button" disabled={busy} onClick={() => { setDraft(null); edit(""); selectRef.current?.focus(); }} className={secondaryButton}>{t("connections.cancel")}</button>
      </div>}
    </form> : <button type="button" disabled={disabled} onClick={() => onConfigure(row, "automation")} className={`${secondaryButton} mt-3 text-primary`}>{t("connections.configureServers")}</button>}
    {error?.id === id && <p role="alert" className="mt-3 break-words text-sm text-error">{error.message}</p>}
  </div>;
}

function OutageMeasurement({ outage, ongoing = false }) {
  const { lang, t } = useI18n();
  const duration = formatOutageDuration(outage.durationMs, lang) || t("common.noData");
  const kind = ["automatic", "manual", "transient"].includes(outage.kind) ? t(`connections.outageKind.${outage.kind}`) : "";
  const firstFailure = outage.startEvidence === "runtime-unavailable" ? "outageTunnelUnavailable"
    : outage.startEvidence === "route-blocked" ? "outageRouteBlocked"
    : outage.startEvidence === "https-failed" ? "outageFirstFailed" : "outageFirstObserved";
  const stages = [
    ["firstFailedAt", firstFailure, outage.fromLocation],
    ["detectedAt", "outageDetected"],
    ["selectedAt", "outageSelected", outage.toLocation],
    ["appliedAt", "outageApplied"],
    ["recoveredAt", "outageRecovered"],
  ];
  return <details>
    <summary className={`cursor-pointer break-words py-1 ${ongoing ? "text-error" : ""}`}>
      {t(ongoing ? "connections.outageOngoing" : "connections.outageCompleted", { duration })}
    </summary>
    <div className="mt-1 space-y-2 border-l border-outline-variant/40 pl-3">
      <p>{t("connections.outageBounds")}</p>
      {kind && <p className="text-on-surface">{kind}</p>}
      <dl className="space-y-1">
        {stages.map(([field, label, location]) => {
          const value = outage[field];
          const time = Date.parse(value);
          if (!Number.isFinite(time)) return null;
          return <div key={field} className="flex flex-wrap justify-between gap-x-3">
            <dt className="min-w-0 break-words">{t(`connections.${label}`)}{location && ` · ${location}`}</dt>
            <dd className="tabular-nums"><time dateTime={value}>{new Date(time).toLocaleString(lang, {
              month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", fractionalSecondDigits: 3,
            })}</time></dd>
          </div>;
        })}
      </dl>
    </div>
  </details>;
}

function SiteList({ source, config, name, multiple, rowId, editing, edit, busy, pending, error, apply, t }) {
  const id = `${rowId}:sites:${source.id}`;
  const [draft, setDraft] = useState(null);
  const [input, setInput] = useState("");
  const [query, setQuery] = useState("");
  const [expanded, setExpanded] = useState(false);
  const [localError, setLocalError] = useState("");
  const addButton = useRef(null);
  const wasEditing = useRef(false);
  const isEditing = editing === id && draft !== null;
  const entries = isEditing ? draft.entries : source.entries;
  const filtered = entries.filter(entry => entry.toLowerCase().includes(query.trim().toLowerCase()));
  const disabled = busy || Boolean(editing && editing !== id);
  const title = multiple ? source.shared ? t("connections.sharedList") : source.name : t("connections.sites");
  const changed = isEditing && (JSON.stringify(draft.entries) !== JSON.stringify(draft.source.entries) || Boolean(input.trim()));
  const preview = [...entries.filter(entry => !/^[\d.:/]+$/.test(entry)), ...entries.filter(entry => /^[\d.:/]+$/.test(entry))].slice(0, 4);

  useEffect(() => {
    if (wasEditing.current && !isEditing) addButton.current?.focus();
    wasEditing.current = isEditing;
  }, [isEditing]);

  function startEdit() {
    setDraft({ source, entries: [...source.entries] });
    setInput("");
    setQuery("");
    setLocalError("");
    edit(id);
  }

  function addToDraft() {
    const added = parseDomainInput(input);
    if (!added.length) { setLocalError(t("connections.siteInputInvalid")); return; }
    setDraft(previous => ({ ...previous, entries: [...new Set([...previous.entries, ...added])] }));
    setInput("");
    setQuery("");
    setLocalError("");
  }

  async function save(event) {
    event.preventDefault();
    const added = parseDomainInput(input);
    if (input.trim() && !added.length) { setLocalError(t("connections.siteInputInvalid")); return; }
    const nextEntries = [...new Set([...draft.entries, ...added])];
    let request;
    try { request = domainSaveRequest(config, draft.source, nextEntries); }
    catch { setLocalError(t("connections.siteListChanged")); return; }
    if (await apply(id, request, t("connections.domainsUpdated"))) { setDraft(null); setInput(""); setQuery(""); }
  }

  return <div className="min-w-0">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <h3 className="text-sm font-medium text-on-surface">{title} <span className="ml-1 text-on-surface-variant">{entries.length}</span></h3>
      {!isEditing && <button ref={addButton} type="button" disabled={disabled} onClick={startEdit} className={`${secondaryButton} -mr-2 text-primary`}><Icon name="add" className="h-4 w-4" />{t("connections.addSite")}</button>}
    </div>
    {!isEditing ? <>
      <div className="mt-2 flex flex-wrap gap-2">
        {(expanded ? [] : preview).map(entry => <span key={entry} className="max-w-full break-all rounded bg-surface-container px-2 py-1 font-mono text-xs text-on-surface-variant">{entry}</span>)}
        {!entries.length && <p className="text-sm text-on-surface-variant">{t("connections.noSitesYet")}</p>}
      </div>
      {entries.length > 0 && <button type="button" disabled={entries.length <= 4 && disabled} aria-expanded={entries.length > 4 ? expanded : undefined} onClick={() => { if (entries.length <= 4) { startEdit(); return; } setExpanded(value => !value); setQuery(""); }} className={`${secondaryButton} -ml-3 mt-1 text-primary`}>{t(entries.length <= 4 ? "connections.editSites" : expanded ? "connections.hideSites" : "connections.viewSites", { count: entries.length })}</button>}
    </> : null}
    {(expanded || isEditing) && <div className="mt-2">
      {(entries.length > 6 || query) && <input type="search" aria-label={t("connections.searchSitesFor", { name })} placeholder={t("connections.sharedDomainsSearch")} value={query} onChange={event => setQuery(event.target.value)} className="mb-2 min-h-11 w-full min-w-0 rounded-lg border border-outline-variant/30 bg-surface-container px-3 py-2 text-sm text-on-surface" />}
      <ul aria-label={t("connections.sitesFor", { name })} className="max-h-48 overflow-y-auto overscroll-contain">
        {filtered.map(entry => <li key={entry} className="flex min-h-10 items-center justify-between gap-2 border-b border-outline-variant/15 py-1">
          <span className="min-w-0 break-all font-mono text-xs text-on-surface">{entry}</span>
          {isEditing && <button type="button" disabled={busy} aria-label={t("connections.removeDomain", { domain: entry })} onClick={() => setDraft(previous => ({ ...previous, entries: previous.entries.filter(item => item !== entry) }))} className="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg text-on-surface-variant hover:bg-error/10 hover:text-error disabled:opacity-40"><Icon name="close" className="h-4 w-4" /></button>}
        </li>)}
      </ul>
      {!filtered.length && <p className="py-2 text-sm text-on-surface-variant">{t(query ? "connections.sharedDomainsNoMatches" : "connections.noSitesYet")}</p>}
      {!isEditing && <button type="button" disabled={disabled} onClick={startEdit} className={`${secondaryButton} -ml-3 text-primary`}>{t("connections.editSites")}</button>}
    </div>}
    {isEditing && <form onSubmit={save} className="mt-3 space-y-3">
      <label className="block text-sm text-on-surface" htmlFor={`site-input-${id}`}>{t("connections.addSite")}</label>
      <div className="flex min-w-0 gap-2">
        <input id={`site-input-${id}`} autoFocus value={input} disabled={busy} onChange={event => { setInput(event.target.value); setLocalError(""); }} onKeyDown={event => { if (event.key === "Enter") { event.preventDefault(); if (input.trim()) addToDraft(); } }} placeholder="example.com" className="min-h-11 w-full min-w-0 rounded-lg border border-outline-variant/30 bg-surface-container px-3 py-2 text-sm text-on-surface" />
        <button type="button" disabled={busy || !input.trim()} onClick={addToDraft} className={`${secondaryButton} border border-outline-variant/30`}>{t("connections.addToList")}</button>
      </div>
      <p className="text-xs text-on-surface-variant">{t("connections.siteDraftHelp")}</p>
      <div className="flex flex-wrap gap-2">
        <button type="submit" disabled={busy || !changed} className={primaryButton}>{pending === id ? t("connections.savingSites") : t("connections.prioritySave")}</button>
        <button type="button" disabled={busy} onClick={() => { setDraft(null); setInput(""); setQuery(""); setLocalError(""); edit(""); }} className={secondaryButton}>{t("connections.cancel")}</button>
      </div>
    </form>}
    {(localError || error?.id === id) && <p role="alert" className="mt-3 break-words text-sm text-error">{localError || error.message}</p>}
  </div>;
}
