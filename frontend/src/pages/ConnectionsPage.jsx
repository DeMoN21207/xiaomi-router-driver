import { useDeferredValue, useEffect, useMemo, useState, useRef, useCallback } from "react";
import { applyRules, fetchJSON, refreshSubscription as requestSubscriptionRefresh } from "../api.js";
import SharedDomainEditor from "../components/SharedDomainEditor.jsx";
import RoutingOverview from "../components/RoutingOverview.jsx";
import SectionTabs from "../components/SectionTabs.jsx";
import ServerPing from "../components/ServerPing.jsx";
import RefreshCountdown from "../components/RefreshCountdown.jsx";
import { startVisiblePolling } from "../visiblePolling.js";
import { parseDomainInput } from "../domainInput.js";
import { useI18n } from "../i18n.jsx";
import Icon from "../components/Icon.jsx";
import InlineNotice from "../components/InlineNotice.jsx";
import { formatLatencyMs, statusToneClasses } from "../utils.js";

const defaultForm = { name: "", type: "openvpn", source: "", enabled: true };
const defaultPriorityDraft = { name: "", ruleId: "", selectionMode: "latency", enabled: true, targets: [], schedule: [] };
const DOMAIN_SEARCH_RESULT_LIMIT = 200;
const ROUTING_ENTRIES_EXAMPLE = "# Example routing entries\n# Lines starting with # are ignored\n# Domains, IPv4 addresses and IPv4 CIDR ranges are supported\n\nyoutube.com\ngooglevideo.com\ndiscord.com\ngateway.discord.gg\n149.154.160.0/20\n91.108.56.0/22\n";

function nameFromSource(source) {
  try {
    const parsed = new URL(source.trim());
    return decodeURIComponent(parsed.hash.replace(/^#/, "")).trim();
  } catch {
    return "";
  }
}

function createDomainHealthMap(payload) {
  return new Map((payload?.domains ?? []).map((entry) => [entry.domain, entry]));
}

function getDomainHealthRecord(domainHealthMap, domain) {
  return domainHealthMap?.get?.(domain) ?? {
    domain,
    directDnsStatus: "unknown",
    directTransportStatus: "unknown",
    vpnDnsStatus: "unknown",
    vpnTransportStatus: "unknown",
    decision: "review",
    consecutiveDirectDnsFailures: 0,
    consecutiveDirectTransportFailures: 0,
    consecutiveVpnDnsFailures: 0,
    consecutiveVpnTransportFailures: 0,
    checksTotal: 0,
    successTotal: 0,
    lastCheckedAt: "",
    lastDirectOkAt: "",
    lastVpnOkAt: "",
    directLastError: "",
    vpnLastError: "",
    lastDirectIps: [],
    lastVpnIps: [],
  };
}

function isDomainPathHealthy(dnsStatus, transportStatus) {
  if (dnsStatus === "static") {
    return true;
  }
  if (dnsStatus !== "ok") {
    return false;
  }
  return ["ok", "unknown", "static"].includes(transportStatus);
}

function matchesDomainHealthFilter(record, filter) {
  const directHealthy = isDomainPathHealthy(record.directDnsStatus, record.directTransportStatus);
  const vpnHealthy = isDomainPathHealthy(record.vpnDnsStatus, record.vpnTransportStatus);

  switch (String(filter || "all")) {
    case "both":
      return directHealthy && vpnHealthy;
    case "direct_only":
      return directHealthy && !vpnHealthy;
    case "vpn_only":
      return !directHealthy && vpnHealthy;
    case "neither":
      return !directHealthy && !vpnHealthy;
    case "candidates":
      return record.decision === "delete_candidate";
    case "unchecked":
      return record.directDnsStatus === "unknown" && record.vpnDnsStatus === "unknown";
    default:
      return true;
  }
}

function getDomainDnsMeta(status, t) {
  switch (String(status || "unknown")) {
    case "ok":
      return { label: t("connections.domainStatusOk"), className: "bg-secondary/10 text-secondary" };
    case "static":
      return { label: t("connections.domainStatusStatic"), className: "bg-outline-variant/15 text-on-surface-variant" };
    case "nxdomain":
      return { label: t("connections.domainStatusNxdomain"), className: "bg-error/10 text-error" };
    case "timeout":
      return { label: t("connections.domainStatusTimeout"), className: "bg-tertiary/10 text-tertiary" };
    case "servfail":
      return { label: t("connections.domainStatusServfail"), className: "bg-tertiary/10 text-tertiary" };
    case "noanswer":
      return { label: t("connections.domainStatusNoanswer"), className: "bg-tertiary/10 text-tertiary" };
    case "error":
      return { label: t("connections.domainStatusError"), className: "bg-error/10 text-error" };
    case "runtime_down":
      return { label: t("connections.domainStatusRuntimeDown"), className: "bg-error/10 text-error" };
    default:
      return { label: t("connections.domainStatusUnknown"), className: "bg-outline-variant/10 text-on-surface-variant" };
  }
}

function getDomainTransportMeta(status, t) {
  switch (String(status || "unknown")) {
    case "ok":
      return { label: t("connections.domainTransportStatusOk"), className: "bg-secondary/10 text-secondary" };
    case "static":
      return { label: t("connections.domainTransportStatusStatic"), className: "bg-outline-variant/15 text-on-surface-variant" };
    case "timeout":
      return { label: t("connections.domainTransportStatusTimeout"), className: "bg-tertiary/10 text-tertiary" };
    case "refused":
      return { label: t("connections.domainTransportStatusRefused"), className: "bg-tertiary/10 text-tertiary" };
    case "unreachable":
      return { label: t("connections.domainTransportStatusUnreachable"), className: "bg-error/10 text-error" };
    case "error":
      return { label: t("connections.domainTransportStatusError"), className: "bg-error/10 text-error" };
    case "runtime_down":
      return { label: t("connections.domainTransportStatusRuntimeDown"), className: "bg-error/10 text-error" };
    default:
      return { label: t("connections.domainTransportStatusUnknown"), className: "bg-outline-variant/10 text-on-surface-variant" };
  }
}

function getDomainDecisionMeta(decision, t) {
  switch (String(decision || "review")) {
    case "keep":
      return { label: t("connections.domainDecisionKeep"), className: "bg-secondary/10 text-secondary" };
    case "delete_candidate":
      return { label: t("connections.domainDecisionDelete"), className: "bg-error/10 text-error" };
    default:
      return { label: t("connections.domainDecisionReview"), className: "bg-tertiary/10 text-tertiary" };
  }
}

function formatDomainHealthTime(value) {
  if (!value) {
    return "";
  }
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return value;
  }
  return parsed.toLocaleString();
}

function DomainPathStatus({ label, dnsStatus, transportStatus, dnsFailures, transportFailures, lastError, t }) {
  const dnsMeta = getDomainDnsMeta(dnsStatus, t);
  const transportMeta = getDomainTransportMeta(transportStatus, t);

  return (
    <div className="space-y-1.5">
      <div className="text-[10px] font-bold uppercase tracking-wide text-outline">{label}</div>
      <div className="flex flex-wrap gap-1.5">
        <span className={`inline-flex rounded-full px-2 py-1 text-[10px] font-bold uppercase tracking-wide ${dnsMeta.className}`}>
          DNS {dnsMeta.label}
        </span>
        <span className={`inline-flex rounded-full px-2 py-1 text-[10px] font-bold uppercase tracking-wide ${transportMeta.className}`}>
          TCP {transportMeta.label}
        </span>
      </div>
      {(dnsFailures > 0 || transportFailures > 0) ? (
        <div className="text-[10px] text-outline-variant">
          {`DNS x${dnsFailures} · TCP x${transportFailures}`}
        </div>
      ) : null}
      {lastError ? (
        <div className="max-w-[220px] truncate text-[10px] text-error/80" title={lastError}>
          {lastError}
        </div>
      ) : null}
    </div>
  );
}

export default function ConnectionsPage() {
  const { t } = useI18n();
  const [config, setConfig] = useState(null);
  const [configLoaded, setConfigLoaded] = useState(false);
  const [status, setStatus] = useState(null);
  const [failoverStatus, setFailoverStatus] = useState(null);
  const [priorityStatus, setPriorityStatus] = useState(null);
  const [, refreshPingClock] = useState(0);
  const [domainHealthMap, setDomainHealthMap] = useState(() => new Map());
  const [form, setForm] = useState(defaultForm);
  const [showForm, setShowForm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [prioritySelection, setPrioritySelection] = useState(null);
  const [prioritySelectionError, setPrioritySelectionError] = useState(null);
  const prioritySelectionLock = useRef(false);
  const [pageError, setPageError] = useState("");
  const [toast, setToast] = useState(null);
  const toastTimer = useRef(null);
  const [probing, setProbing] = useState(false);
  const [probeResult, setProbeResult] = useState(null);
  const [uploading, setUploading] = useState(false);
  const [refreshingProviderId, setRefreshingProviderId] = useState("");
  const fileInputRef = useRef(null);

  const [section, setSection] = useState("active");
  const [editorMode, setEditorMode] = useState("domains");
  const [selectedProviderId, setSelectedProviderId] = useState("");
  const [selectedRuleId, setSelectedRuleId] = useState("");

  const providers = config?.providers ?? [];
  const rules = config?.rules ?? [];
  const priorityPolicies = config?.priorityPolicies ?? [];
  const sharedDomains = config?.sharedDomains;
  const runtimeMap = new Map((status?.providers ?? []).map((p) => [p.id, p]));
  const failoverOverrideMap = new Map((failoverStatus?.activeOverrides ?? []).map((item) => [item.ruleId, item]));
  const failoverProviderMap = new Map((failoverStatus?.providers ?? []).map((item) => [item.providerId, item]));
  const priorityStatusMap = new Map((priorityStatus?.policies ?? []).map((item) => [item.policyId, item]));
  const activeFailoverOverrides = failoverStatus?.activeOverrides ?? [];
  const currentProviderId = providers.some(provider => provider.id === selectedProviderId) ? selectedProviderId : providers[0]?.id;

  function configureRoute(row, mode = "domains") {
    setSelectedProviderId(row.providerId);
    setSelectedRuleId(row.ruleId || "");
    setEditorMode(mode);
    setSection("routing");
  }

  const showToast = useCallback((message, error = false) => {
    setToast({ message, error });
    clearTimeout(toastTimer.current);
    toastTimer.current = setTimeout(() => setToast(null), 2800);
  }, []);

  useEffect(() => () => clearTimeout(toastTimer.current), []);

  const refresh = useCallback(async () => {
    let configReady = false;
    try {
      const [nextConfig, nextRules] = await Promise.all([fetchJSON("/api/config"), fetchJSON("/api/rules")]);
      setConfig({ ...nextConfig, effectiveRules: nextRules.effectiveRules });
      setConfigLoaded(true);
      configReady = true;
      setPageError("");
    } catch (error) {
      setConfigLoaded(true);
      setPageError(error.message);
      return;
    }

    const [nextStatus, nextDomainHealth, nextFailoverStatus, nextPriorityStatus] = await Promise.allSettled([
      fetchJSON("/api/status"),
      fetchJSON("/api/domains/health"),
      fetchJSON("/api/failover/status"),
      fetchJSON("/api/priority-policies/status"),
    ]);

    if (nextStatus.status === "fulfilled") {
      setStatus(nextStatus.value);
    } else if (configReady) {
      setPageError(nextStatus.reason?.message || t("error.connectionsLoad"));
    }

    if (nextDomainHealth.status === "fulfilled") {
      setDomainHealthMap(createDomainHealthMap(nextDomainHealth.value));
    } else if (configReady) {
      setPageError(nextDomainHealth.reason?.message || t("error.connectionsLoad"));
    }

    if (nextFailoverStatus.status === "fulfilled") {
      setFailoverStatus(nextFailoverStatus.value);
    }

    if (nextPriorityStatus.status === "fulfilled") {
      setPriorityStatus(nextPriorityStatus.value);
    }
  }, [t]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    if (section === "catalog") return;
    let cancelled = false;
    let pending = false;
    const timer = setInterval(async () => {
      // Expire displayed measurements even when every status request fails.
      refreshPingClock(value => value + 1);
      if (pending) return;
      pending = true;
      try {
        const results = await Promise.allSettled([fetchJSON("/api/status", { trackLoading: false }), fetchJSON("/api/priority-policies/status", { trackLoading: false }), fetchJSON("/api/failover/status", { trackLoading: false })]);
        if (cancelled) return;
        if (results[0].status === "fulfilled") setStatus(results[0].value);
        if (results[1].status === "fulfilled") setPriorityStatus(results[1].value);
        if (results[2].status === "fulfilled") setFailoverStatus(results[2].value);
      } finally { pending = false; }
    }, 15_000);
    return () => { cancelled = true; clearInterval(timer); };
  }, [section]);

  async function changePriorityServer(policy, location) {
    if (prioritySelectionLock.current || busy || !policy.enabled) return;
    prioritySelectionLock.current = true;
    setBusy(true);
    setPrioritySelection({ policyId: policy.id, location });
    setPrioritySelectionError(null);
    try {
      await fetchJSON(`/api/priority-policies/${encodeURIComponent(policy.id)}/override`, {
        method: location === null ? "DELETE" : "POST",
        ...(location === null ? {} : { headers: { "Content-Type": "application/json" }, body: JSON.stringify({ location }) }),
        timeoutMs: 150_000,
      });
      await refresh();
      showToast(t(location === null ? "connections.priorityOverrideCleared" : "connections.locationUpdated"));
    } catch (error) {
      setPrioritySelectionError({ policyId: policy.id, message: error.message });
      setPageError(error.message);
      showToast(error.message, true);
      await refresh();
    } finally {
      prioritySelectionLock.current = false;
      setPrioritySelection(null);
      setBusy(false);
    }
  }

  async function saveSharedDomains(change) {
    setBusy(true);
    try {
      await fetchJSON("/api/config/shared-domains", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(change),
        timeoutMs: 150_000,
      });
      await refresh();
      showToast(t("connections.domainsUpdated"));
      return true;
    } catch (error) {
      await refresh();
      setPageError(error.message);
      showToast(error.message, true);
    } finally {
      setBusy(false);
    }
  }

  async function uploadOvpn(event) {
    const file = event.target.files?.[0];
    if (!file) return;
    setUploading(true);
    try {
      const body = new FormData();
      body.append("file", file);
      const result = await fetchJSON("/api/providers/upload", { method: "POST", body });
      setForm((prev) => ({ ...prev, source: result.path }));
      setProbeResult(null);
      showToast(t("connections.uploaded"));
    } catch (error) {
      showToast(error.message, true);
    } finally {
      setUploading(false);
      if (fileInputRef.current) fileInputRef.current.value = "";
    }
  }

  async function probeSource() {
    if (!form.source.trim()) return;
    setProbing(true);
    setProbeResult(null);
    try {
      const result = await fetchJSON("/api/providers/probe", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ type: form.type, source: form.source }),
      });
      setProbeResult(result);
    } catch (error) {
      setProbeResult({ locations: [], error: error.message });
    } finally {
      setProbing(false);
    }
  }

  async function submit(event) {
    event.preventDefault();
    setBusy(true);
    try {
      const payload = {
        name: form.name,
        type: form.type,
        source: form.source,
        enabled: form.enabled,
      };
      await fetchJSON("/api/providers", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      setForm(defaultForm);
      setProbeResult(null);
      setShowForm(false);
      setPageError("");
      showToast(t("connections.added"));
      await refresh();
    } catch (error) {
      const message = error.message;
      setPageError(message);
      showToast(message, true);
    } finally {
      setBusy(false);
    }
  }

  async function remove(id) {
    setBusy(true);
    try {
      await fetchJSON(`/api/providers/${encodeURIComponent(id)}`, { method: "DELETE" });
      setPageError("");
      showToast(t("connections.deleted"));
      await refresh();
    } catch (error) {
      const message = error.message;
      setPageError(message);
      showToast(message, true);
    } finally {
      setBusy(false);
    }
  }

  async function updateProvider(provider, patch) {
    setBusy(true);
    try {
      await fetchJSON(`/api/providers/${encodeURIComponent(provider.id)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: provider.name,
          type: provider.type,
          source: provider.source,
          selectedLocation: provider.selectedLocation || "",
          enabled: patch.enabled ?? provider.enabled,
        }),
      });
		await applyRules();
      setPageError("");
      showToast(patch.enabled ? t("connections.providerEnabled") : t("connections.providerDisabled"));
      await refresh();
    } catch (error) {
      const message = error.message;
      setPageError(message);
      showToast(message, true);
    } finally {
      setBusy(false);
    }
  }

  async function refreshSubscription(provider) {
    setRefreshingProviderId(provider.id);
    try {
      const result = await requestSubscriptionRefresh(provider.id);
      setPageError("");
      showToast(t("connections.subscriptionRefreshed", { count: String(result.entries ?? 0) }));
      await refresh();
    } catch (error) {
      const message = error.message;
      setPageError(message);
      showToast(message, true);
    } finally {
      setRefreshingProviderId("");
    }
  }

  return (
    <div className="min-w-0 space-y-8 overflow-x-clip">
      <header className="flex flex-col justify-between gap-6 md:flex-row md:items-end">
        <div>
          <h1 className="font-headline text-3xl font-bold tracking-tight text-on-surface">{t("connections.workspaceTitle")}</h1>
          <p className="mt-2 max-w-2xl text-sm text-on-surface-variant">{t("connections.workspaceHelp")}</p>
        </div>
        <div className="flex items-center gap-3">
          {section === "catalog" ? <button
            type="button"
            onClick={() => setShowForm((value) => !value)}
            className="flex items-center gap-2 rounded-lg bg-primary px-5 py-3 text-sm font-semibold text-on-primary transition-colors hover:bg-primary/90"
          >
            <Icon name={showForm ? "close" : "add"} className="h-5 w-5" />
            {showForm ? t("connections.close") : t("connections.add")}
          </button> : section === "active" ? <button type="button" disabled={busy} onClick={async () => { setBusy(true); try { await refresh(); } finally { setBusy(false); } }} className="inline-flex items-center gap-2 rounded-lg border border-outline-variant/30 px-4 py-2 text-sm text-on-surface hover:bg-surface-container-high disabled:opacity-40"><Icon name="sync" className="h-4 w-4" />{t("connections.refreshStatus")}</button> : null}
        </div>
      </header>

      <SectionTabs idPrefix="connection-sections" label={t("connections.workspaceTitle")} value={section} onChange={setSection} items={[
        { id: "active", label: t("connections.sectionActive"), shortLabel: t("connections.sectionActiveShort") },
        { id: "routing", label: t("connections.sectionRouting"), shortLabel: t("connections.sectionRoutingShort") },
        { id: "catalog", label: t("connections.sectionCatalog"), shortLabel: t("connections.sectionCatalogShort") },
      ]} />

      {pageError ? <InlineNotice tone="error" title={t("error.connections")} message={pageError} /> : null}
      {activeFailoverOverrides.length > 0 ? (
        <InlineNotice
          tone="warn"
          title={t("connections.failoverActive")}
          message={formatFailoverSummary(activeFailoverOverrides, t)}
        />
      ) : null}

      {section === "catalog" && showForm && (
        <form onSubmit={submit} className="space-y-5 rounded-xl border border-outline-variant/10 bg-surface-container-low p-6">
          <h2 className="font-headline text-lg font-bold text-on-surface">{t("connections.newProvider")}</h2>
          <div className="grid grid-cols-1 gap-5 md:grid-cols-2">
            <FormField label={t("connections.name")} hint={t("connections.nameHint")}>
              <input
                className="w-full rounded-lg border-none bg-surface-container-highest px-4 py-3 text-sm text-on-surface focus:ring-1 focus:ring-primary"
                value={form.name}
                onChange={(event) => setForm({ ...form, name: event.target.value })}
                placeholder={t("connections.namePlaceholder")}
              />
            </FormField>
            <FormField label={t("connections.type")} hint={t("connections.typeHint")}>
              <select
                className="w-full rounded-lg border-none bg-surface-container-highest px-4 py-3 text-sm text-on-surface focus:ring-1 focus:ring-primary"
                value={form.type}
                onChange={(event) => setForm({ ...form, type: event.target.value })}
              >
                <option value="openvpn">{t("common.openvpn")}</option>
                <option value="subscription">{t("common.subscription")}</option>
              </select>
            </FormField>
            <FormField
              label={t("connections.source")}
              hint={t(form.type === "openvpn" ? "connections.sourceHintOpenvpn" : "connections.sourceHintSubscription")}
            >
              <div className="flex gap-2">
                <input
                  className="w-full rounded-lg border-none bg-surface-container-highest px-4 py-3 font-mono text-sm text-on-surface focus:ring-1 focus:ring-primary"
                  value={form.source}
                  onChange={(event) => {
                    const source = event.target.value;
                    setForm((prev) => ({ ...prev, source, name: prev.name.trim() ? prev.name : nameFromSource(source) }));
                    setProbeResult(null);
                  }}
                  placeholder={form.type === "openvpn" ? "profiles/de.ovpn" : "https://provider/sublink/..."}
                />
                {form.type === "openvpn" && (
                  <>
                    <input ref={fileInputRef} type="file" accept=".ovpn" onChange={uploadOvpn} className="hidden" />
                    <button
                      type="button"
                      onClick={() => fileInputRef.current?.click()}
                      disabled={uploading}
                      className="shrink-0 rounded-lg bg-surface-container-highest px-4 py-3 text-sm font-medium text-primary transition-colors hover:bg-surface-container-high disabled:opacity-40"
                      title={t("connections.upload")}
                    >
                      <Icon name="upload_file" className="h-5 w-5" />
                    </button>
                  </>
                )}
                <button
                  type="button"
                  onClick={probeSource}
                  disabled={probing || !form.source.trim()}
                  className="shrink-0 rounded-lg bg-surface-container-highest px-4 py-3 text-sm font-medium text-primary transition-colors hover:bg-surface-container-high disabled:opacity-40"
                >
                  {probing ? t("connections.probing") : t("connections.probe")}
                </button>
              </div>
            </FormField>
          </div>
          {probeResult && (
            <div className="rounded-lg border border-outline-variant/10 bg-surface-container p-4">
              <h3 className="mb-2 text-xs font-bold uppercase tracking-widest text-outline">
                {t("connections.probeLocations")}
                {probeResult.locations?.length > 0 && (
                  <span className="ml-2 font-mono text-primary">{probeResult.locations.length} {t("connections.probeCount")}</span>
                )}
              </h3>
              {probeResult.error && (
                <p className="mb-2 text-sm text-error">{probeResult.error}</p>
              )}
              {probeResult.locations?.length > 0 ? (
                <LocationsBrowser locations={probeResult.locations} t={t} />
              ) : !probeResult.error ? (
                <p className="text-sm text-on-surface-variant">{t("connections.probeEmpty")}</p>
              ) : null}
            </div>
          )}
          <div className="flex items-center justify-between gap-4 max-sm:flex-col max-sm:items-start">
            <label className="flex cursor-pointer items-center gap-3">
              <input
                type="checkbox"
                checked={form.enabled}
                onChange={(event) => setForm({ ...form, enabled: event.target.checked })}
                className="h-5 w-5 rounded border-outline-variant bg-surface-container-highest text-primary focus:ring-0"
              />
              <span className="text-sm font-medium">{t("connections.enabled")}</span>
            </label>
            <button
              type="submit"
              disabled={busy}
              className="rounded-xl bg-gradient-to-r from-primary to-primary-container px-8 py-2.5 text-sm font-bold text-on-primary shadow-lg shadow-primary/10 transition-all active:scale-95 disabled:opacity-50"
            >
              {t("connections.submit")}
            </button>
          </div>
        </form>
      )}

      {section === "active" && configLoaded && <div role="tabpanel" id="connection-sections-panel-active" aria-labelledby="connection-sections-tab-active">
        <RoutingOverview config={config} statusMap={priorityStatusMap} failoverMap={failoverOverrideMap} busy={busy} onChanged={refresh} onConfigure={configureRoute} showToast={showToast} setPageError={setPageError} t={t} />
      </div>}

      {section !== "active" && <div role="tabpanel" id={`connection-sections-panel-${section}`} aria-labelledby={`connection-sections-tab-${section}`} className="space-y-6">
      {section === "routing" && <div className="space-y-6">
      <SectionTabs idPrefix="routing-editors" label={t("connections.sectionRouting")} value={editorMode} onChange={setEditorMode} items={[
        { id: "domains", label: t("connections.routeDomains"), shortLabel: t("connections.routeDomainsShort") },
        { id: "automation", label: t("connections.routeAutomation"), shortLabel: t("connections.routeAutomationShort") },
      ]} />
      {sharedDomains && editorMode === "domains" && (
        <SharedDomainEditor
          list={sharedDomains}
          busy={busy}
          onSave={saveSharedDomains}
          t={t}
        />
      )}
      </div>}

      {section !== "active" && providers.length > 1 && <nav aria-label={t("connections.chooseSubscription")} className="flex flex-wrap gap-2">
        {providers.map(provider => <button key={provider.id} type="button" aria-current={provider.id === currentProviderId ? "true" : undefined} onClick={() => setSelectedProviderId(provider.id)} className={`rounded-lg border px-4 py-2 text-sm ${provider.id === currentProviderId ? "border-primary/40 bg-primary/10 text-primary" : "border-outline-variant/20 text-on-surface-variant hover:bg-surface-container-high"}`}>{provider.name}</button>)}
      </nav>}

      <div role={section === "routing" ? "tabpanel" : undefined} id={section === "routing" ? `routing-editors-panel-${editorMode}` : undefined} aria-labelledby={section === "routing" ? `routing-editors-tab-${editorMode}` : undefined} className="space-y-6">
        {providers.filter(provider => provider.id === currentProviderId).map((provider) => {
          const runtime = runtimeMap.get(provider.id);
          const isOnline = provider.enabled && runtime?.health === "ready";
          const tone = !provider.enabled ? "outline" : isOnline ? "secondary" : runtime?.health === "warning" ? "tertiary" : "error";
          const statusLabel = !provider.enabled ? t("connections.off") : isOnline ? t("connections.online") : t("connections.problem");
          const toneClasses = statusToneClasses(tone);
          const providerRules = rules.filter((r) => r.providerId === provider.id);
          const providerPriorityPolicies = priorityPolicies.filter((policy) => policy.providerId === provider.id);

          return (
            <ProviderCard
              key={provider.id}
              mode={section === "catalog" ? "catalog" : editorMode}
              selectedRuleId={selectedRuleId}
              onSelectRule={setSelectedRuleId}
              onEditRoute={(ruleId) => configureRoute({ providerId: provider.id, ruleId })}
              provider={provider}
              providers={providers}
              rules={rules}
              toneClasses={toneClasses}
              statusLabel={statusLabel}
              isOnline={isOnline}
              providerRules={providerRules}
              priorityPolicies={providerPriorityPolicies}
              priorityStatusMap={priorityStatusMap}
              prioritySelection={prioritySelection}
              prioritySelectionError={prioritySelectionError}
              onPriorityServerChange={changePriorityServer}
              busy={busy}
              sharedDomains={sharedDomains}
              onSharedDomainsChange={saveSharedDomains}
              failoverOverrides={failoverOverrideMap}
              failoverProvider={failoverProviderMap.get(provider.id)}
              domainHealthMap={domainHealthMap}
              onDelete={() => remove(provider.id)}
              onToggleEnabled={(nextEnabled) => updateProvider(provider, { enabled: nextEnabled })}
              onRefreshSubscription={() => refreshSubscription(provider)}
              refreshingProviderId={refreshingProviderId}
              onDomainsChange={refresh}
              showToast={showToast}
              setPageError={setPageError}
              t={t}
            />
          );
        })}
      </div>
      </div>}

      {!configLoaded && (
        <div className="rounded-xl bg-surface-container-low p-12 text-center">
          <Icon name="vpn_lock" className="mx-auto mb-4 h-12 w-12 animate-pulse text-outline-variant" />
          <p className="text-on-surface-variant">{t("common.loading")}</p>
        </div>
      )}

      {configLoaded && providers.length === 0 && (
        <div className="rounded-xl bg-surface-container-low p-12 text-center">
          <Icon name="vpn_lock" className="mx-auto mb-4 h-12 w-12 text-outline-variant" />
          <p className="text-on-surface-variant">{t("connections.empty")}</p>
        </div>
      )}

      {toast && (
        <div
          className={`fixed right-6 bottom-6 z-50 rounded-xl px-5 py-3 text-sm font-medium shadow-2xl ${
            toast.error ? "bg-error-container text-on-error" : "bg-secondary-container text-on-secondary"
          }`}
        >
          {toast.message}
        </div>
      )}
    </div>
  );
}



function formatFailoverSummary(overrides, t) {
  if (!Array.isArray(overrides) || overrides.length === 0) {
    return "";
  }
  const preview = overrides.slice(0, 3).map((item) => {
    const route = item.ruleName || item.ruleId || "-";
    if (item.mode === "direct") {
      return `${route}: ${t("connections.failoverNowDirect")}`;
    }
    const target = [item.activeProviderName, item.activeLocation].filter(Boolean).join(" / ");
    return `${route}: ${target || item.activeProviderId || "-"}`;
  });
  const extra = Math.max(0, overrides.length - preview.length);
  return extra > 0 ? `${preview.join("; ")}; +${extra}` : preview.join("; ");
}

function formatFailoverOverrideMessage(override, t) {
  if (!override) {
    return "";
  }
  const original = [override.originalProviderName, override.originalLocation].filter(Boolean).join(" / ");
  if (override.mode === "direct") {
    return t("connections.failoverDirect", { from: original || override.originalProviderId || "-" });
  }
  const active = [override.activeProviderName, override.activeLocation].filter(Boolean).join(" / ");
  return t("connections.failoverProvider", { from: original || override.originalProviderId || "-", to: active || override.activeProviderId || "-" });
}

function ProviderCard({ mode, selectedRuleId, onSelectRule, onEditRoute, provider, providers, rules, toneClasses, statusLabel, isOnline, providerRules, priorityPolicies, priorityStatusMap, prioritySelection, prioritySelectionError, onPriorityServerChange, busy, sharedDomains, onSharedDomainsChange, failoverOverrides, failoverProvider, domainHealthMap, onDelete, onToggleEnabled, onRefreshSubscription, refreshingProviderId, onDomainsChange, showToast, setPageError, t }) {
  const [saving, setSaving] = useState(false);
  const [probing, setProbing] = useState(false);
  const [refreshingLatency, setRefreshingLatency] = useState(false);
  const [probeLocations, setProbeLocations] = useState(null);
  const [sourceVisible, setSourceVisible] = useState(false);
  const [editingSource, setEditingSource] = useState(false);
  const [sourceDraft, setSourceDraft] = useState(provider.source);
  const [savingSource, setSavingSource] = useState(false);
  const [newRouteLoc, setNewRouteLoc] = useState("");
  const [livePings, setLivePings] = useState({});
  const [livePingCheckedAt, setLivePingCheckedAt] = useState("");
  const [livePingFailed, setLivePingFailed] = useState(false);
  const [livePollState, setLivePollState] = useState({ phase: "paused", nextRunAt: null, intervalMs: 10_000 });

  const sharedPrimary = sharedDomains?.ruleIds?.find(id => providerRules.some(rule => rule.id === id && rule.enabled));
  const implicitTargets = sharedPrimary && sharedDomains.entries?.length && !priorityPolicies.some(policy => policy.ruleId === sharedPrimary)
    ? sharedDomains.ruleIds.map(id => providerRules.find(rule => rule.id === id && rule.enabled)?.selectedLocation).filter(Boolean) : [];
  const liveNames = new Set(mode === "automation"
    ? [...priorityPolicies.flatMap(policy => (policy.targets || []).map(target => target.location)), ...implicitTargets]
    : providerRules.map(rule => rule.selectedLocation));
  const liveLocationsKey = JSON.stringify((probeLocations || []).filter(location => liveNames.has(location.name)).map(({ name, address, type, endpointError }) => ({ name, address, type, endpointError })));
  const fallbackPings = new Map([...priorityStatusMap.values()].filter(policy => policy.providerId === provider.id).flatMap(policy => (policy.targets || []).map(target => [target.location, target])));
  const pingForLocation = location => livePings[location] || fallbackPings.get(location);

  useEffect(() => {
    setLivePings({});
    setLivePingCheckedAt("");
    setLivePingFailed(false);
    setLivePollState({ phase: "paused", nextRunAt: null, intervalMs: 10_000 });
    if (mode === "catalog") return;
    const locations = JSON.parse(liveLocationsKey);
    if (!locations.length) return;
    return startVisiblePolling(async signal => {
      try {
        const result = await fetchJSON("/api/providers/latency", {
          method: "POST", signal, timeoutMs: 8_000, trackLoading: false,
          headers: { "Content-Type": "application/json" }, body: JSON.stringify({ locations }),
        });
        if (signal.aborted) return;
        const checkedAt = new Date().toISOString();
        const next = Object.fromEntries((result.locations || []).map(location => [location.name, { pingMs: location.latencyMs || 0, pingError: location.latencyError || location.endpointError || "", pingCheckedAt: checkedAt, pingLive: true }]));
        setLivePings(previous => ({ ...previous, ...next }));
        setLivePingCheckedAt(checkedAt);
        setLivePingFailed(false);
      } catch {
        if (!signal.aborted) setLivePingFailed(true);
      }
    }, { onState: setLivePollState });
  }, [mode, liveLocationsKey, provider.source, provider.type]);

  const activeRuleId = providerRules.some(rule => rule.id === selectedRuleId) ? selectedRuleId : providerRules[0]?.id;
  const refreshingSubscription = provider.type === "subscription" && refreshingProviderId === provider.id;
  const providerActionBusy = Boolean(refreshingProviderId);

  // Reload the catalogue once when its source changes, independently of ping ticks.
  useEffect(() => {
    const controller = new AbortController();
    setProbing(true);
    setProbeLocations(null);
    void (async () => {
      try {
        const result = await fetchJSON("/api/providers/probe", {
          method: "POST", signal: controller.signal, trackLoading: false,
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ type: provider.type, source: provider.source }),
        });
        if (!controller.signal.aborted) setProbeLocations(result.locations || []);
      } catch (error) {
        if (!controller.signal.aborted) showToast(error.message, true);
      } finally {
        if (!controller.signal.aborted) setProbing(false);
      }
    })();
    return () => controller.abort();
  }, [provider.id, provider.source, provider.type, showToast]);

  async function applyAndRefresh() {
    try {
		await applyRules();
    } finally {
      await onDomainsChange();
    }
  }

  async function addRoute() {
    if (!newRouteLoc) return;
    const existing = providerRules.find(rule => rule.selectedLocation === newRouteLoc);
    if (existing) { onEditRoute(existing.id); return; }
    setSaving(true);
    try {
      const result = await fetchJSON("/api/rules", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: `${provider.name} — ${newRouteLoc}`,
          providerId: provider.id,
          selectedLocation: newRouteLoc,
          domains: "",
          enabled: true,
        }),
      });
      setNewRouteLoc("");
      showToast(t("connections.routeAdded"));
      await onDomainsChange();
      onEditRoute(result.rule.id);
    } catch (error) {
      setPageError(error.message);
      showToast(error.message, true);
    } finally {
      setSaving(false);
    }
  }

  async function refreshLatency() {
    if (!probeLocations || probeLocations.length === 0) return;

    setRefreshingLatency(true);
    try {
      const result = await fetchJSON("/api/providers/latency", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ locations: probeLocations }),
      });
      setProbeLocations(result.locations || []);
      showToast(t("connections.pingUpdated"));
    } catch (error) {
      setPageError(error.message);
      showToast(error.message, true);
    } finally {
      setRefreshingLatency(false);
    }
  }

  async function deleteRule(ruleId) {
    setSaving(true);
    try {
      await fetchJSON(`/api/rules/${encodeURIComponent(ruleId)}`, { method: "DELETE" });
      await applyAndRefresh();
    } catch (error) {
      setPageError(error.message);
      showToast(error.message, true);
    } finally {
      setSaving(false);
    }
  }

  // Locations already used in rules (for highlighting in chips)
  const usedLocations = new Set(providerRules.map((r) => r.selectedLocation).filter(Boolean));
  const selectedNewRoute = probeLocations?.find((loc) => loc.name === newRouteLoc);
  const newRouteEndpointError = selectedNewRoute?.endpointError || "";

  return (
    <div className="overflow-hidden rounded-2xl border border-outline-variant/20 bg-surface-container-low">
      {mode === "catalog" && <div className="flex flex-wrap items-center justify-between gap-5 p-5 sm:p-6">
        <div className="flex min-w-0 flex-1 items-start gap-4 text-left">
          <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary"><Icon name="vpn_lock" className="h-5 w-5" /></span>
          <span className="min-w-0">
            <span className="flex flex-wrap items-center gap-3">
              <span className="font-headline text-xl font-bold tracking-tight text-on-surface">{provider.name}</span>
              <span className={`inline-flex items-center gap-1.5 text-xs font-medium ${toneClasses.text || "text-on-surface-variant"}`}>
                <span className={`h-1.5 w-1.5 rounded-full ${toneClasses.dot}`} />{statusLabel}
              </span>
            </span>
            <span className="mt-1.5 block text-xs text-on-surface-variant">
              {t(`common.${provider.type}`)} · {t("connections.catalogServerCount", { count: probeLocations?.length ?? providerRules.length })}
            </span>
          </span>
        </div>
        <div className="flex w-full flex-wrap items-center justify-end gap-3 border-t border-outline-variant/15 pt-4 sm:w-auto sm:border-0 sm:pt-0">
          {provider.type === "subscription" && (
            <button type="button" onClick={onRefreshSubscription} disabled={busy || saving || providerActionBusy} className="inline-flex items-center gap-2 rounded-lg px-3 py-2 text-sm font-medium text-on-surface-variant transition-colors hover:bg-surface-container-high disabled:opacity-40">
              <Icon name="sync" className={`h-4 w-4 ${refreshingSubscription ? "animate-spin" : ""}`} />
              {refreshingSubscription ? t("connections.refreshingSubscription") : t("connections.refreshSubscription")}
            </button>
          )}
          <button type="button" role="switch" aria-checked={provider.enabled} aria-label={provider.enabled ? t("connections.disableProvider") : t("connections.enableProvider")} onClick={() => onToggleEnabled(!provider.enabled)} disabled={busy || saving || providerActionBusy} className="inline-flex items-center gap-2 py-2 text-sm text-on-surface-variant disabled:opacity-40">
            <span className={`relative h-5 w-9 rounded-full transition-colors ${provider.enabled ? "bg-secondary" : "bg-outline-variant/50"}`}><span className={`absolute top-0.5 left-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform motion-reduce:transition-none ${provider.enabled ? "translate-x-4" : "translate-x-0"}`} /></span>
            {provider.enabled ? t("connections.providerActive") : t("connections.providerInactive")}
          </button>
        </div>
      </div>}

      <div className="border-t border-outline-variant/20 p-4 sm:p-6">
        {mode !== "catalog" && <div className="mb-5"><h2 className="font-headline text-lg font-semibold text-on-surface">{provider.name}</h2><p className="mt-1 text-sm text-on-surface-variant">{t(mode === "domains" ? "connections.domainWorkspaceHelp" : "connections.automationWorkspaceHelp")}</p>{(probing || liveLocationsKey !== "[]") && <RefreshCountdown state={livePollState} loading={probing} failed={livePingFailed} checkedAt={livePingCheckedAt} t={t} />}</div>}
        {mode === "domains" && <div className="grid gap-5 md:grid-cols-[220px_minmax(0,1fr)]">
          <nav aria-label={t("connections.chooseRoute")} className="flex gap-2 overflow-x-auto md:flex-col">
            {providerRules.map(rule => <button type="button" key={rule.id} aria-current={rule.id === activeRuleId ? "true" : undefined} onClick={() => onSelectRule(rule.id)} className={`min-w-44 rounded-lg border p-3 text-left md:min-w-0 ${rule.id === activeRuleId ? "border-primary/30 bg-primary/10 text-primary" : "border-outline-variant/15 text-on-surface-variant hover:bg-surface-container-high"}`}>
              <span className="block text-sm font-medium">{rule.selectedLocation || rule.name}</span>
              <span className="mt-1 block"><ServerPing target={pingForLocation(rule.selectedLocation)} t={t} /></span>
              <span className="mt-1 block text-xs text-on-surface-variant">{t(sharedDomains?.ruleIds?.includes(rule.id) ? "connections.sharedAndOwn" : "connections.ownOnly")}</span>
            </button>)}
          </nav>
          <div className="min-w-0">
          {/* Rules (routes) — each with its own location + domains */}
          <div className="space-y-4">
            {providerRules.length === 0 && (
              <p className="text-sm text-on-surface-variant">{t("connections.noRoutes")}</p>
            )}
            {providerRules.filter(rule => rule.id === activeRuleId).map((rule) => (
              <RuleBlock
                key={rule.id}
                rule={rule}
                serverPing={pingForLocation(rule.selectedLocation)}
                provider={provider}
                sharedDomains={sharedDomains}
                sharedBusy={busy}
                onSharedDomainsChange={onSharedDomainsChange}
                probeLocations={probeLocations}
                probing={probing}
                saving={saving}
                setSaving={setSaving}
                failoverOverride={failoverOverrides?.get(rule.id)}
                domainHealthMap={domainHealthMap}
                onDeleteRule={() => deleteRule(rule.id)}
                onDomainsChange={onDomainsChange}
                showToast={showToast}
                setPageError={setPageError}
                t={t}
              />
            ))}
          </div>


          </div>
        </div>}
        {mode === "automation" && <>
          {provider.type === "subscription" && (
            <PriorityPoliciesPanel
              provider={provider}
              providerRules={providerRules}
              policies={priorityPolicies}
              sharedDomains={sharedDomains}
              priorityStatusMap={priorityStatusMap}
              livePings={livePings}
              probeLocations={probeLocations}
              probing={probing}
              pendingSelection={prioritySelection}
              selectionError={prioritySelectionError}
              onSelectServer={onPriorityServerChange}
              saving={saving || busy}
              setSaving={setSaving}
              onChanged={onDomainsChange}
              showToast={showToast}
              setPageError={setPageError}
              t={t}
            />
          )}


        </>}
        {mode === "catalog" && <>
          {/* Source info */}
          <div className="mb-5">
            <span className="mb-1 block text-[10px] uppercase tracking-widest text-on-surface-variant">{t("connections.source")}</span>
            {editingSource ? (
              <div className="flex gap-2">
                <input
                  className="w-full rounded-lg border border-outline-variant/20 bg-surface-container-lowest/50 px-3 py-2 font-mono text-sm text-on-surface outline-none transition-colors focus:border-primary/40"
                  value={sourceDraft}
                  onChange={(e) => setSourceDraft(e.target.value)}
                  disabled={savingSource}
                />
                <button
                  type="button"
                  disabled={savingSource || sourceDraft.trim() === provider.source}
                  onClick={async () => {
                    setSavingSource(true);
                    try {
                      await fetchJSON(`/api/providers/${encodeURIComponent(provider.id)}`, {
                        method: "PUT",
                        headers: { "Content-Type": "application/json" },
                        body: JSON.stringify({ name: provider.name, type: provider.type, source: sourceDraft.trim(), enabled: provider.enabled }),
                      });
                      showToast(t("connections.sourceUpdated"));
                      setEditingSource(false);
                      setProbeLocations(null);
                      await onDomainsChange();
                    } catch (err) {
                      showToast(err.message, true);
                    } finally {
                      setSavingSource(false);
                    }
                  }}
                  className="shrink-0 rounded-lg bg-primary/10 px-3 py-2 text-sm font-medium text-primary transition-colors hover:bg-primary/20 disabled:opacity-40"
                >
                  <Icon name="check" className="h-4 w-4" />
                </button>
                <button
                  type="button"
                  onClick={() => { setEditingSource(false); setSourceDraft(provider.source); }}
                  disabled={savingSource}
                  className="shrink-0 rounded-lg bg-surface-container-highest px-3 py-2 text-sm text-on-surface-variant transition-colors hover:text-error disabled:opacity-40"
                >
                  <Icon name="close" className="h-4 w-4" />
                </button>
              </div>
            ) : (
              <div className="flex items-center gap-2">
                <code className="block w-full overflow-hidden text-ellipsis rounded-lg bg-surface-container-lowest/50 px-3 py-2 font-mono text-sm text-on-surface">
                  {sourceVisible ? provider.source : "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022"}
                </code>
                <button
                  type="button"
                  onClick={() => setSourceVisible((v) => !v)}
                  className="shrink-0 rounded-lg bg-surface-container-highest p-2 text-on-surface-variant transition-colors hover:text-primary"
                  title={sourceVisible ? t("connections.hideSource") : t("connections.showSource")}
                >
                  <Icon name={sourceVisible ? "visibility_off" : "visibility"} className="h-4 w-4" />
                </button>
                <button
                  type="button"
                  onClick={() => { setEditingSource(true); setSourceDraft(provider.source); }}
                  className="shrink-0 rounded-lg bg-surface-container-highest p-2 text-on-surface-variant transition-colors hover:text-primary"
                  title={t("connections.editSource")}
                >
                  <Icon name="edit" className="h-4 w-4" />
                </button>
              </div>
            )}
          </div>

          {/* Loading indicator while probing */}
          {probing && (
            <div className="mb-5 flex items-center gap-2 text-sm text-on-surface-variant">
              <Icon name="travel_explore" className="h-4 w-4 animate-spin text-primary" />
              {t("connections.probing")}
            </div>
          )}

          {/* Location chips + add route */}
          {probeLocations && probeLocations.length > 0 && (
            <div className="mb-5 rounded-lg border border-outline-variant/10 bg-surface-container p-3">
              <h4 className="mb-2 text-xs font-bold uppercase tracking-widest text-outline">
                {t("connections.probeLocations")}
                <span className="ml-2 font-mono text-primary">{probeLocations.length}</span>
              </h4>
              <LocationsBrowser
                locations={probeLocations}
                selectedLocation={newRouteLoc}
                onSelect={setNewRouteLoc}
                usedLocations={usedLocations}
                disabled={saving}
                t={t}
              />
              <div className="mt-3 flex items-center justify-between gap-3 max-sm:flex-col max-sm:items-stretch">
                <div className="min-h-[20px] text-sm text-on-surface-variant">
                  {newRouteLoc ? (
                    <span className={newRouteEndpointError ? "text-error" : ""}>
                      {t("connections.selectedLocation")} <span className="font-medium text-on-surface">{newRouteLoc}</span>
                      {newRouteEndpointError ? ` · ${newRouteEndpointError}` : ""}
                    </span>
                  ) : (
                    t("connections.addRoutePlaceholder")
                  )}
                </div>
                <div className="flex shrink-0 items-center gap-2 max-sm:flex-col max-sm:items-stretch">
                  <button
                    type="button"
                    onClick={refreshLatency}
                    disabled={saving || probing || refreshingLatency || probeLocations.length === 0}
                    className="rounded-lg border border-outline-variant/20 bg-surface-container-highest px-4 py-2 text-sm font-medium text-on-surface transition-colors hover:border-primary/30 hover:text-primary disabled:opacity-40"
                  >
                    {refreshingLatency ? t("connections.refreshingPing") : t("connections.refreshPing")}
                  </button>
                  <button
                    type="button"
                    onClick={addRoute}
                    disabled={saving || !newRouteLoc || Boolean(newRouteEndpointError)}
                    className="rounded-lg bg-primary/10 px-4 py-2 text-sm font-medium text-primary transition-colors hover:bg-primary/20 disabled:opacity-40"
                  >
                    {t("connections.configureRoute")}
                  </button>
                </div>
              </div>
            </div>
          )}


          {/* Delete provider */}
          <div className="mt-4 flex justify-end border-t border-outline-variant/10 pt-4">
            <button
              type="button"
              onClick={onDelete}
              disabled={busy}
              className="flex items-center gap-2 rounded-lg px-3 py-2 text-sm text-on-surface-variant transition-colors hover:bg-error-container/20 hover:text-error disabled:opacity-50"
              title={t("common.deleteProvider")}
            >
              <Icon name="delete" className="h-4 w-4" />
              {t("common.deleteProvider")}
            </button>
          </div>

        </>}
      </div>
    </div>
  );
}

function PriorityPoliciesPanel({ provider, providerRules, policies, sharedDomains, priorityStatusMap, livePings, probeLocations, probing, pendingSelection, selectionError, onSelectServer, saving, setSaving, onChanged, showToast, setPageError, t }) {
  const [editingId, setEditingId] = useState("");
  const [draft, setDraft] = useState(defaultPriorityDraft);
  const [newTarget, setNewTarget] = useState("");
  const [dragIndex, setDragIndex] = useState(-1);

  const locationNames = useMemo(() => (probeLocations || []).filter((loc) => !loc.endpointError).map((loc) => loc.name), [probeLocations]);
  const sharedPrimary = sharedDomains?.ruleIds?.find(id => providerRules.some(rule => rule.id === id && rule.enabled));
  const implicitPolicy = sharedPrimary && sharedDomains.entries?.length && !policies.some(policy => policy.ruleId === sharedPrimary) ? {
    id: "shared-domain-list", ruleId: sharedPrimary, name: t("connections.sharedList"), selectionMode: priorityStatusMap?.get("shared-domain-list")?.selectionMode || "latency", enabled: true, synthetic: true,
    targets: sharedDomains.ruleIds.map(id => providerRules.find(rule => rule.id === id && rule.enabled)?.selectedLocation).filter(Boolean).map(location => ({ location })), schedule: [],
  } : null;
  const visiblePolicies = implicitPolicy ? [implicitPolicy, ...policies] : policies;
  const editing = editingId !== "";

  function startCreate() {
    setEditingId("new");
    setDraft({
      ...defaultPriorityDraft,
      name: `${provider.name} priority`,
      ruleId: providerRules.find(rule => !policies.some(policy => policy.enabled && (!policy.ruleId || policy.ruleId === rule.id)))?.id || providerRules[0]?.id || "",
      targets: [],
      schedule: [],
    });
    setNewTarget("");
  }

  function startEdit(policy) {
    setEditingId(policy.synthetic ? "new" : policy.id);
    setDraft({
      name: policy.name || "",
      ruleId: policy.ruleId || "",
      selectionMode: policy.selectionMode || "priority",
      enabled: policy.enabled !== false,
      targets: (policy.targets || []).map((target) => target.location).filter(Boolean),
      schedule: (policy.schedule || []).map((item) => ({ start: item.start || "", end: item.end || "", location: item.location || "" })),
    });
    setNewTarget("");
  }

  function cancelEdit() {
    setEditingId("");
    setDraft(defaultPriorityDraft);
    setNewTarget("");
  }

  function addTarget() {
    const location = newTarget.trim();
    if (!location || draft.targets.some((target) => target.toLowerCase() === location.toLowerCase())) {
      return;
    }
    setDraft((current) => ({ ...current, targets: [...current.targets, location] }));
    setNewTarget("");
  }

  function moveTarget(from, to) {
    if (to < 0 || to >= draft.targets.length) return;
    const nextTargets = [...draft.targets];
    const [item] = nextTargets.splice(from, 1);
    nextTargets.splice(to, 0, item);
    setDraft((current) => ({ ...current, targets: nextTargets }));
  }

  function removeTarget(location) {
    setDraft((current) => ({
      ...current,
      targets: current.targets.filter((target) => target !== location),
      schedule: current.schedule.filter((window) => window.location !== location),
    }));
  }

  function updateSchedule(index, patch) {
    setDraft((current) => {
      const schedule = [...current.schedule];
      schedule[index] = { ...schedule[index], ...patch };
      return { ...current, schedule };
    });
  }

  async function savePolicy(event) {
    event.preventDefault();
    const payload = {
      name: draft.name.trim(),
      providerId: provider.id,
      ruleId: draft.ruleId,
      selectionMode: draft.selectionMode,
      enabled: Boolean(draft.enabled),
      targets: draft.targets.map((location) => ({ location })),
      schedule: draft.schedule
        .filter((window) => window.start || window.end || window.location)
        .map((window) => ({ start: window.start, end: window.end, location: window.location })),
    };
    setSaving(true);
    try {
      const isNew = editingId === "new";
      await fetchJSON(isNew ? "/api/priority-policies" : `/api/priority-policies/${encodeURIComponent(editingId)}`, {
        method: isNew ? "POST" : "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
		await applyRules();
      cancelEdit();
      showToast(t("connections.prioritySaved"));
      await onChanged();
    } catch (error) {
      setPageError(error.message);
      showToast(error.message, true);
    } finally {
      setSaving(false);
    }
  }

  async function deletePolicy(policy) {
    setSaving(true);
    try {
      await fetchJSON(`/api/priority-policies/${encodeURIComponent(policy.id)}`, { method: "DELETE" });
		await applyRules();
      showToast(t("connections.priorityDeleted"));
      await onChanged();
    } catch (error) {
      setPageError(error.message);
      showToast(error.message, true);
    } finally {
      setSaving(false);
    }
  }


  return (
    <div className="space-y-5">
      <div className="mb-3 flex items-center justify-between gap-3">
        <div>
          <h3 className="font-headline text-base font-semibold text-on-surface">{t("connections.priorityPolicies")}</h3>
          <p className="mt-1 text-sm text-on-surface-variant">{t("connections.priorityHint")}</p>
          <p className="mt-2 text-xs text-on-surface-variant">{t("connections.priorityClickHelp")}</p>
        </div>
        {!editing && <button
          type="button"
          onClick={startCreate}
          disabled={saving || editing}
          className="shrink-0 rounded-lg bg-primary/10 px-3 py-2 text-sm font-medium text-primary transition-colors hover:bg-primary/20 disabled:opacity-40"
        >
          <Icon name="add" className="mr-1 inline h-4 w-4 align-[-3px]" />
          {t("connections.priorityAdd")}
        </button>}
      </div>

      {visiblePolicies.length === 0 && !editing ? (
        <p className="rounded-lg border border-dashed border-outline-variant/20 bg-surface-container-high px-4 py-3 text-sm text-on-surface-variant">
          {t("connections.priorityEmpty")}
        </p>
      ) : null}

      {!editing && <div className="space-y-3">
        {visiblePolicies.map((policy) => {
          const status = priorityStatusMap?.get(policy.id);
          const active = status?.activeLocation || "";
          const override = status?.overrideLocation || "";
          const policyName = policy.ruleId === sharedPrimary ? t("connections.sharedList") : policy.name;
          const changing = pendingSelection?.policyId === policy.id;
          return (
            <div key={policy.id} role="group" aria-label={policyName} className="rounded-lg border border-outline-variant/10 bg-surface-container-high p-3">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-headline text-sm font-bold text-on-surface">{policyName}</span>
                    <span className="text-xs text-on-surface-variant">{t(policy.selectionMode === "latency" ? "connections.selectionByPing" : "connections.selectionByOrder")}</span>
                    <span className={`rounded-full px-2 py-0.5 text-[10px] font-bold uppercase tracking-wide ${policy.enabled ? "bg-secondary/10 text-secondary" : "bg-outline-variant/10 text-outline"}`}>
                      {policy.enabled ? t("connections.priorityEnabled") : t("connections.priorityDisabled")}
                    </span>
                    {active ? (
                      <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wide text-primary">
                        {t("connections.priorityActive")}: {active}
                      </span>
                    ) : null}
                    {override ? (
                      <span className="rounded-full bg-tertiary/10 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wide text-tertiary">
                        {t("connections.priorityOverride")}: {override}
                      </span>
                    ) : null}
                  </div>
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {(policy.targets || []).map((target, index) => {
                      const targetStatus = status?.targets?.find((item) => item.location === target.location);
                      const targetActive = active === target.location;
                      const targetPending = changing && pendingSelection.location === target.location;
                      return (
                        <button
                          key={`${policy.id}:${target.location}`}
                          type="button"
                          aria-label={t("connections.switchPolicyServer", { name: policyName, server: target.location })}
                          aria-pressed={targetActive}
                          aria-busy={targetPending}
                          disabled={saving || !policy.enabled}
                          onClick={() => onSelectServer(policy, target.location)}
                          className={`cursor-pointer rounded-lg border px-2.5 py-1 text-left text-xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/60 focus-visible:ring-offset-2 focus-visible:ring-offset-surface-container-high disabled:cursor-default disabled:opacity-40 ${targetActive ? "border-primary/30 bg-primary/10 text-primary" : "border-outline-variant/15 bg-surface-container-highest text-on-surface-variant hover:border-primary/25 hover:text-on-surface"}`}
                        >
                          {index + 1}. {target.location}
                          {targetStatus?.status === "healthy" ? ` · ${t("connections.serverAvailable")}` : targetStatus?.status === "unhealthy" ? ` · ${t("connections.serverUnavailable")}` : ""}
                          {targetPending ? <span className="ml-2 inline-flex items-center gap-1" role="status"><Icon name="sync" className="h-3 w-3 animate-spin motion-reduce:animate-none" />{t("connections.switchingServer")}</span> : <span className="ml-2"><ServerPing target={livePings[target.location] || targetStatus} t={t} /></span>}
                        </button>
                      );
                    })}
                  </div>
                  {override ? <button type="button" aria-label={t("connections.returnPolicyToAuto", { name: policyName })} disabled={saving || !policy.enabled} onClick={() => onSelectServer(policy, null)} className="mt-3 inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-xs font-medium text-primary hover:bg-primary/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/60 disabled:opacity-40">{changing && pendingSelection.location === null ? <><Icon name="sync" className="h-3 w-3 animate-spin motion-reduce:animate-none" /><span role="status">{t("connections.returningToAuto")}</span></> : t("connections.returnToAuto")}</button> : null}
                  {selectionError?.policyId === policy.id ? <p role="alert" className="mt-2 text-sm text-error">{selectionError.message}</p> : null}
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <button type="button" aria-label={t("connections.editPriority", { name: policy.name })} onClick={() => startEdit(policy)} disabled={saving || editing} className="rounded-lg bg-surface-container-highest p-2 text-on-surface-variant transition-colors hover:text-primary disabled:opacity-40">
                    <Icon name="edit" className="h-4 w-4" />
                  </button>
                  {!policy.synthetic && <button type="button" aria-label={t("connections.deletePriority", { name: policy.name })} onClick={() => deletePolicy(policy)} disabled={saving || editing} className="rounded-lg bg-surface-container-highest p-2 text-on-surface-variant transition-colors hover:text-error disabled:opacity-40">
                    <Icon name="delete" className="h-4 w-4" />
                  </button>}
                </div>
              </div>
            </div>
          );
        })}
      </div>}

      {editing ? (
        <form onSubmit={savePolicy} className="mt-4 space-y-4 rounded-lg border border-primary/20 bg-surface-container-high p-4">
          <div className="grid gap-4 md:grid-cols-[minmax(0,1fr)_160px]">
            <FormField label={t("connections.priorityName")}>
              <input
                autoFocus
                value={draft.name}
                onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))}
                className="w-full rounded-lg border border-outline-variant/20 bg-surface-container-lowest/50 px-3 py-2 text-sm text-on-surface outline-none transition-colors focus:border-primary/40"
              />
            </FormField>
            <label className="flex items-end gap-2 pb-2 text-sm text-on-surface">
              <input
                type="checkbox"
                checked={draft.enabled}
                onChange={(event) => setDraft((current) => ({ ...current, enabled: event.target.checked }))}
                className="h-4 w-4 accent-primary"
              />
              {t("connections.priorityEnabled")}
            </label>
          </div>

          <FormField label={t("connections.priorityScope")}>
            <select value={draft.ruleId} onChange={event => setDraft(current => ({ ...current, ruleId: event.target.value }))} className="w-full rounded-lg border border-outline-variant/30 bg-surface-container px-3 py-2 text-sm text-on-surface">
              <option value="">{t("connections.priorityWholeProvider")}</option>
              {providerRules.map(rule => <option key={rule.id} value={rule.id}>{rule.id === sharedPrimary ? t("connections.sharedList") : policies.find(policy => policy.ruleId === rule.id)?.name || rule.name || rule.selectedLocation}</option>)}
            </select>
          </FormField>

          <FormField label={t("connections.selectionMode")} hint={t("connections.selectionModeHelp")}>
            <select value={draft.selectionMode} onChange={event => setDraft(current => ({ ...current, selectionMode: event.target.value }))} className="w-full rounded-lg border border-outline-variant/30 bg-surface-container px-3 py-2 text-sm text-on-surface">
              <option value="latency">{t("connections.selectionByPing")}</option>
              <option value="priority">{t("connections.selectionByOrder")}</option>
            </select>
          </FormField>

          <div>
            <p className="mb-2 text-[10px] font-bold uppercase tracking-widest text-outline">{t("connections.priorityTargets")}</p>
            <div className="mb-2 flex gap-2">
              <select
                aria-label={t("connections.priorityTargets")}
                value={newTarget}
                onChange={(event) => setNewTarget(event.target.value)}
                disabled={probing || !probeLocations}
                className="min-w-0 flex-1 rounded-lg border border-outline-variant/20 bg-surface-container-lowest/50 px-3 py-2 text-sm text-on-surface outline-none transition-colors focus:border-primary/40 disabled:opacity-50"
              >
                <option value="">{probing ? t("connections.probing") : t("connections.prioritySelectTarget")}</option>
                {locationNames.map((location) => (
                  <option key={location} value={location}>{location}</option>
                ))}
              </select>
              <button type="button" onClick={addTarget} disabled={!newTarget} className="rounded-lg bg-primary/10 px-3 py-2 text-sm font-medium text-primary transition-colors hover:bg-primary/20 disabled:opacity-40">
                {t("connections.priorityAddTarget")}
              </button>
            </div>
            <div className="space-y-1.5">
              {draft.targets.map((location, index) => (
                <div
                  key={location}
                  draggable
                  onDragStart={() => setDragIndex(index)}
                  onDragOver={(event) => event.preventDefault()}
                  onDrop={() => {
                    if (dragIndex >= 0 && dragIndex !== index) moveTarget(dragIndex, index);
                    setDragIndex(-1);
                  }}
                  className="flex items-center gap-2 rounded-lg border border-outline-variant/15 bg-surface-container-lowest/50 px-3 py-2"
                >
                  <span className="w-6 text-xs font-bold text-outline">{index + 1}</span>
                  <span className="min-w-0 flex-1 truncate text-sm text-on-surface">{location}</span>
                  <ServerPing target={livePings[location] || priorityStatusMap?.get(editingId)?.targets?.find(target => target.location === location)} t={t} />
                  <button type="button" aria-label={t("connections.moveServerUp", { location })} onClick={() => moveTarget(index, index - 1)} disabled={index === 0} className="rounded p-1 text-on-surface-variant hover:text-primary disabled:opacity-30">
                    <Icon name="arrow_drop_up" className="h-4 w-4" />
                  </button>
                  <button type="button" aria-label={t("connections.moveServerDown", { location })} onClick={() => moveTarget(index, index + 1)} disabled={index === draft.targets.length - 1} className="rounded p-1 text-on-surface-variant hover:text-primary disabled:opacity-30">
                    <Icon name="arrow_drop_down" className="h-4 w-4" />
                  </button>
                  <button type="button" aria-label={t("connections.removeServerFromOrder", { location })} onClick={() => removeTarget(location)} className="rounded p-1 text-on-surface-variant hover:text-error">
                    <Icon name="close" className="h-4 w-4" />
                  </button>
                </div>
              ))}
            </div>
          </div>

          <div>
            <div className="mb-2 flex items-center justify-between gap-3">
              <p className="text-[10px] font-bold uppercase tracking-widest text-outline">{t("connections.prioritySchedule")}</p>
              <button type="button" onClick={() => setDraft((current) => ({ ...current, schedule: [...current.schedule, { start: "09:00", end: "18:00", location: current.targets[0] || "" }] }))} disabled={draft.targets.length === 0} className="rounded-lg bg-surface-container-highest px-2.5 py-1 text-xs text-primary transition-colors hover:bg-primary/10 disabled:opacity-40">
                {t("connections.priorityAddWindow")}
              </button>
            </div>
            <div className="space-y-2">
              {draft.schedule.map((window, index) => (
                <div key={`${index}:${window.start}:${window.end}`} className="grid gap-2 sm:grid-cols-[120px_120px_minmax(0,1fr)_32px]">
                  <input type="time" value={window.start} onChange={(event) => updateSchedule(index, { start: event.target.value })} className="rounded-lg border border-outline-variant/20 bg-surface-container-lowest/50 px-3 py-2 text-sm text-on-surface outline-none focus:border-primary/40" />
                  <input type="time" value={window.end} onChange={(event) => updateSchedule(index, { end: event.target.value })} className="rounded-lg border border-outline-variant/20 bg-surface-container-lowest/50 px-3 py-2 text-sm text-on-surface outline-none focus:border-primary/40" />
                  <select value={window.location} onChange={(event) => updateSchedule(index, { location: event.target.value })} className="rounded-lg border border-outline-variant/20 bg-surface-container-lowest/50 px-3 py-2 text-sm text-on-surface outline-none focus:border-primary/40">
                    <option value="">{t("connections.prioritySelectTarget")}</option>
                    {draft.targets.map((location) => <option key={location} value={location}>{location}</option>)}
                  </select>
                  <button type="button" onClick={() => setDraft((current) => ({ ...current, schedule: current.schedule.filter((_, itemIndex) => itemIndex !== index) }))} className="rounded-lg text-on-surface-variant transition-colors hover:bg-error-container/20 hover:text-error">
                    <Icon name="close" className="mx-auto h-4 w-4" />
                  </button>
                </div>
              ))}
              {draft.schedule.length === 0 ? (
                <p className="text-xs text-on-surface-variant">{t("connections.priorityNoSchedule")}</p>
              ) : null}
            </div>
          </div>

          <div className="flex justify-end gap-2">
            <button type="button" onClick={cancelEdit} disabled={saving} className="rounded-lg px-4 py-2 text-sm text-on-surface-variant transition-colors hover:text-on-surface disabled:opacity-40">
              {t("connections.cancel")}
            </button>
            <button type="submit" disabled={saving || !draft.name.trim() || draft.targets.length === 0} className="rounded-lg bg-primary/10 px-4 py-2 text-sm font-medium text-primary transition-colors hover:bg-primary/20 disabled:opacity-40">
              {t("connections.prioritySave")}
            </button>
          </div>
        </form>
      ) : null}
    </div>
  );
}

function LocationsBrowser({ locations, selectedLocation = "", onSelect, usedLocations, disabled = false, t }) {
  const [query, setQuery] = useState("");
  const [sortKey, setSortKey] = useState("");
  const [sortDirection, setSortDirection] = useState("asc");
  const deferredQuery = useDeferredValue(query);
  const normalizedQuery = deferredQuery.trim().toLowerCase();
  const selectedSet = usedLocations ?? new Set();

  const filteredLocations = useMemo(() => {
    if (!normalizedQuery) {
      return locations;
    }

    return locations.filter((loc) => {
      const haystack = [loc.name, loc.address, loc.type].filter(Boolean).join(" ").toLowerCase();
      return haystack.includes(normalizedQuery);
    });
  }, [locations, normalizedQuery]);

  const visibleLocations = useMemo(() => {
    if (!sortKey) {
      return filteredLocations;
    }

    return [...filteredLocations].sort((left, right) => compareLocations(left, right, sortKey, sortDirection));
  }, [filteredLocations, sortDirection, sortKey]);

  function toggleSort(nextSortKey) {
    if (sortKey === nextSortKey) {
      setSortDirection((currentDirection) => (currentDirection === "asc" ? "desc" : "asc"));
      return;
    }

    setSortKey(nextSortKey);
    setSortDirection("asc");
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3 max-sm:flex-col max-sm:items-stretch">
        <label className="relative block flex-1">
          <Icon name="search" className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-outline-variant" />
          <input
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t("connections.locationSearchPlaceholder")}
            className="w-full rounded-lg border border-outline-variant/15 bg-surface-container-highest py-2 pl-9 pr-3 text-sm text-on-surface outline-none transition-colors focus:border-primary/40"
          />
        </label>
        <div className="shrink-0 rounded-full bg-surface-container-highest px-3 py-1 text-xs font-medium text-on-surface-variant">
          {t("connections.locationsShowing", { visible: String(filteredLocations.length), total: String(locations.length) })}
        </div>
      </div>

      {visibleLocations.length > 0 ? (
        <>
          <div className="overflow-hidden rounded-lg border border-outline-variant/10">
            <div className="grid grid-cols-[110px_minmax(180px,1fr)_minmax(220px,1.3fr)_110px] gap-px bg-outline-variant/10 text-[11px] font-bold uppercase tracking-widest text-outline">
              <div className="bg-surface-container-high px-3 py-2">{t("connections.locationTypeColumn")}</div>
              <SortHeader
                label={t("connections.locationAddressColumn")}
                columnKey="address"
                activeSortKey={sortKey}
                direction={sortDirection}
                onToggle={toggleSort}
              />
              <SortHeader
                label={t("connections.locationNameColumn")}
                columnKey="name"
                activeSortKey={sortKey}
                direction={sortDirection}
                onToggle={toggleSort}
              />
              <SortHeader
                label={t("connections.locationPingColumn")}
                columnKey="ping"
                activeSortKey={sortKey}
                direction={sortDirection}
                onToggle={toggleSort}
                align="right"
              />
            </div>
            <div className="max-h-72 overflow-auto bg-surface-container-lowest/40">
              {visibleLocations.map((loc) => {
                const active = selectedLocation === loc.name;
                const alreadyUsed = selectedSet.has(loc.name);
                const interactive = typeof onSelect === "function";
                const endpointError = loc.endpointError || "";
                const canSelect = interactive && !disabled && !endpointError;

                return (
                  <button
                    key={`row:${loc.type || "location"}:${loc.name}:${loc.address || ""}`}
                    type="button"
                    onClick={canSelect ? () => onSelect(loc.name) : undefined}
                    disabled={!interactive || disabled || Boolean(endpointError)}
                    title={endpointError || undefined}
                    className={`grid w-full grid-cols-[110px_minmax(180px,1fr)_minmax(220px,1.3fr)_110px] gap-px border-t border-outline-variant/10 text-left text-sm transition-colors ${
                      interactive ? "hover:bg-surface-container/70 disabled:cursor-default disabled:hover:bg-transparent" : "cursor-default"
                    } ${active ? "bg-primary/8" : "bg-transparent"} ${endpointError ? "opacity-60" : ""}`}
                  >
                    <div className="px-3 py-2.5 text-on-surface-variant">
                      <span className="rounded bg-surface-container-highest px-2 py-0.5 text-[11px] font-medium uppercase tracking-wide text-on-surface">
                        {formatLocationType(loc.type)}
                      </span>
                    </div>
                    <div className="px-3 py-2.5 font-mono text-xs text-on-surface">{loc.address || "—"}</div>
                    <div className="px-3 py-2.5 text-on-surface">
                      <div className="flex items-center gap-2">
                        <span className="truncate">{loc.name}</span>
                        {active ? (
                          <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wide text-primary">
                            {t("connections.locationSelected")}
                          </span>
                        ) : null}
                        {alreadyUsed ? (
                          <span className="rounded-full bg-secondary/10 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wide text-secondary">
                            {t("connections.locationUsed")}
                          </span>
                        ) : null}
                        {endpointError ? (
                          <span className="rounded-full bg-error-container/40 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wide text-error">
                            {t("connections.locationUnavailable")}
                          </span>
                        ) : null}
                      </div>
                    </div>
                    <div
                      className="px-3 py-2.5 text-right font-mono text-xs"
                      title={loc.latencyError || undefined}
                    >
                      <LocationLatency location={loc} t={t} />
                    </div>
                  </button>
                );
              })}
            </div>
          </div>
        </>
      ) : (
        <div className="rounded-lg border border-dashed border-outline-variant/20 bg-surface-container-highest/40 px-4 py-5 text-sm text-on-surface-variant">
          {t("connections.locationNoMatch")}
        </div>
      )}
    </div>
  );
}

function SortHeader({ label, columnKey, activeSortKey, direction, onToggle, align = "left" }) {
  const active = activeSortKey === columnKey;

  return (
    <button
      type="button"
      onClick={() => onToggle(columnKey)}
      className={`flex items-center gap-1.5 bg-surface-container-high px-3 py-2 text-left transition-colors hover:bg-surface-container-highest ${
        align === "right" ? "justify-end text-right" : "justify-between"
      }`}
    >
      <span>{label}</span>
      <span className="relative flex flex-col items-center">
        <Icon
          name="arrow_drop_up"
          className={`h-3.5 w-3.5 -mb-1 transition-colors ${
            active && direction === "asc" ? "text-primary" : "text-outline-variant/40"
          }`}
        />
        <Icon
          name="arrow_drop_down"
          className={`h-3.5 w-3.5 -mt-1 transition-colors ${
            active && direction === "desc" ? "text-primary" : "text-outline-variant/40"
          }`}
        />
      </span>
    </button>
  );
}

function compareLocations(left, right, sortKey, sortDirection) {
  const direction = sortDirection === "desc" ? -1 : 1;

  if (sortKey === "ping") {
    const leftMissing = !(left?.latencyMs > 0);
    const rightMissing = !(right?.latencyMs > 0);

    if (leftMissing !== rightMissing) {
      return leftMissing ? 1 : -1;
    }

    if (!leftMissing && left.latencyMs !== right.latencyMs) {
      return (left.latencyMs - right.latencyMs) * direction;
    }

    return compareText(left?.name, right?.name);
  }

  if (sortKey === "address") {
    const byAddress = compareText(left?.address, right?.address);
    if (byAddress !== 0) {
      return byAddress * direction;
    }

    return compareText(left?.name, right?.name);
  }

  if (sortKey === "name") {
    const byName = compareText(left?.name, right?.name);
    if (byName !== 0) {
      return byName * direction;
    }

    return compareText(left?.address, right?.address);
  }

  return 0;
}

function compareText(left, right) {
  return String(left || "").localeCompare(String(right || ""), undefined, {
    numeric: true,
    sensitivity: "base",
  });
}

function LocationLatency({ location, t }) {
  if (location?.endpointError) {
    return <span className="text-error">{t("connections.locationUnavailableShort")}</span>;
  }
  const ms = location?.latencyMs;
  if (!(ms > 0)) {
    return <span className="text-on-surface-variant">—</span>;
  }
  const colorClass = ms < 50 ? "text-secondary" : ms < 150 ? "text-tertiary" : "text-error";
  const dot = ms < 50 ? "●" : ms < 150 ? "●" : "●";
  return (
    <span className={`inline-flex items-center justify-end gap-1 ${colorClass}`}>
      <span className="text-[8px] opacity-70">{dot}</span>
      {formatLatencyMs(ms, t)}
    </span>
  );
}

function formatLocationType(type) {
  const normalized = String(type || "").trim().toLowerCase();
  if (!normalized) {
    return "node";
  }
  return normalized;
}


function RuleBlock({ rule, serverPing, provider, sharedDomains, sharedBusy, onSharedDomainsChange, probeLocations, probing, saving, setSaving, failoverOverride, domainHealthMap, onDeleteRule, onDomainsChange, showToast, setPageError, t }) {
  const [domainInput, setDomainInput] = useState("");
  const [showDomainTools, setShowDomainTools] = useState(false);
  const [changingLocation, setChangingLocation] = useState(false);
  const [newLocation, setNewLocation] = useState("");
  const fileInputRef = useRef(null);
  const selectedNewLocation = probeLocations?.find((loc) => loc.name === newLocation);
  const newLocationEndpointError = selectedNewLocation?.endpointError || "";
  const sharedOrder = sharedDomains?.ruleIds?.indexOf(rule.id) ?? -1;
  const usesSharedDomains = sharedOrder >= 0;

  async function applyLocationChange() {
    if (!newLocation || newLocation === rule.selectedLocation) {
      setChangingLocation(false);
      return;
    }
    setSaving(true);
    try {
      await fetchJSON(`/api/rules/${encodeURIComponent(rule.id)}?apply=1`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: rule.name,
          providerId: provider.id,
          selectedLocation: newLocation,
          domains: rule.domains.join(","),
          enabled: rule.enabled,
        }),
      });
      setChangingLocation(false);
      setNewLocation("");
      showToast(t("connections.locationUpdated"));
      await onDomainsChange();
    } catch (error) {
      setPageError(error.message);
      showToast(error.message, true);
    } finally {
      setSaving(false);
    }
  }

  async function saveDomains(merged) {
    setSaving(true);
    try {
      await fetchJSON(`/api/rules/${encodeURIComponent(rule.id)}?apply=1`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: rule.name, providerId: provider.id, selectedLocation: rule.selectedLocation, domains: merged.join(","), enabled: rule.enabled }),
      });
      setDomainInput("");
      await onDomainsChange();
      showToast(t("connections.domainsUpdated"));
    } catch (error) {
      setPageError(error.message);
      showToast(error.message, true);
    } finally {
      setSaving(false);
    }
  }

  async function addDomains(event) {
    event.preventDefault();
    const raw = domainInput.trim();
    if (!raw) return;
    const merged = [...new Set([...rule.domains, ...parseDomainInput(raw)])];
    await saveDomains(merged);
  }

  function handleFileImport(event) {
    const file = event.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = async (e) => {
      const text = e.target?.result || "";
      const imported = parseDomainInput(text);
      if (imported.length === 0) return;
      const merged = [...new Set([...rule.domains, ...imported])];
      await saveDomains(merged);
    };
    reader.readAsText(file);
    event.target.value = "";
  }

  async function removeDomain(domain) {
    await saveDomains(rule.domains.filter((entry) => entry !== domain));
  }

  async function removeDomains(domainsToRemove) {
    const removeSet = new Set(domainsToRemove);
    await saveDomains(rule.domains.filter((entry) => !removeSet.has(entry)));
  }

  async function checkDomainsHealth(domainsToCheck) {
    if (!Array.isArray(domainsToCheck) || domainsToCheck.length === 0) {
      return;
    }

    setSaving(true);
    try {
      const result = await fetchJSON("/api/domains/health/check", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ domains: domainsToCheck }),
      });
      await onDomainsChange();
      showToast(t("connections.domainHealthCheckedNow", { count: String(result?.count ?? domainsToCheck.length) }));
    } catch (error) {
      setPageError(error.message);
      showToast(error.message, true);
    } finally {
      setSaving(false);
    }
  }

  return (
    <section aria-label={rule.selectedLocation || rule.name} className="rounded-xl border border-outline-variant/20 bg-surface-container-low p-4 sm:p-5">
      <div className="mb-4 flex items-start justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2 min-w-0">
          <Icon name="location_on" className="h-4 w-4 shrink-0 text-primary" />
          <span className="min-w-0 break-words font-headline text-base font-semibold text-on-surface">{rule.selectedLocation || rule.name}</span>
          <ServerPing target={serverPing} t={t} />
          <button
            type="button"
            onClick={() => { setChangingLocation((v) => !v); setNewLocation(rule.selectedLocation || ""); }}
            disabled={saving}
            title={t("connections.changeLocation")}
            className={`shrink-0 rounded px-1.5 py-0.5 text-xs font-medium transition-colors disabled:opacity-40 ${changingLocation ? "bg-primary/15 text-primary" : "bg-surface-container-highest text-on-surface-variant hover:bg-primary/10 hover:text-primary"}`}
          >
            <Icon name="swap_horiz" className="h-4 w-4" />
          </button>
        </div>
        <button
          type="button"
          onClick={onDeleteRule}
          disabled={saving}
          className="text-outline-variant transition-colors hover:text-error disabled:opacity-40"
          title={t("connections.deleteRoute")}
        >
          <Icon name="close" className="h-4 w-4" />
        </button>
      </div>

      {failoverOverride ? (
        <div className="mb-3 rounded-lg border border-tertiary/25 bg-tertiary/10 p-3 text-xs text-tertiary">
          <div className="mb-1 flex items-center gap-1.5 font-bold uppercase tracking-wide">
            <Icon name="swap_horiz" className="h-3.5 w-3.5" />
            {t("connections.failoverRouteActive")}
          </div>
          <div className="text-on-surface-variant">{formatFailoverOverrideMessage(failoverOverride, t)}</div>
          {failoverOverride.reason ? (
            <div className="mt-1 font-mono text-[10px] text-outline-variant">{failoverOverride.reason}</div>
          ) : null}
        </div>
      ) : null}

      {changingLocation && (
        <div className="mb-3 rounded-lg border border-primary/20 bg-surface-container-high p-3">
          <p className="mb-2 text-xs font-bold uppercase tracking-widest text-outline">{t("connections.changeLocation")}</p>
          {probing && !probeLocations ? (
            <div className="flex items-center gap-2 py-2 text-sm text-on-surface-variant">
              <Icon name="travel_explore" className="h-4 w-4 animate-spin text-primary" />
              {t("connections.probing")}
            </div>
          ) : probeLocations && probeLocations.length > 0 ? (
            <>
              <LocationsBrowser
                locations={probeLocations}
                selectedLocation={newLocation}
                onSelect={setNewLocation}
                usedLocations={new Set()}
                disabled={saving}
                t={t}
              />
              <div className="mt-2 flex items-center justify-end gap-2">
                <button
                  type="button"
                  onClick={() => setChangingLocation(false)}
                  disabled={saving}
                  className="rounded-lg px-3 py-1.5 text-sm text-on-surface-variant transition-colors hover:text-on-surface disabled:opacity-40"
                >
                  {t("connections.cancel")}
                </button>
                <button
                  type="button"
                  onClick={applyLocationChange}
                  disabled={saving || !newLocation || newLocation === rule.selectedLocation || Boolean(newLocationEndpointError)}
                  className="rounded-lg bg-primary/10 px-3 py-1.5 text-sm font-medium text-primary transition-colors hover:bg-primary/20 disabled:opacity-40"
                >
                  {saving ? t("connections.changingLocation") : t("connections.applyLocation")}
                </button>
              </div>
              {newLocationEndpointError ? (
                <p className="mt-2 text-right text-xs text-error">{newLocationEndpointError}</p>
              ) : null}
            </>
          ) : (
            <p className="py-2 text-sm text-on-surface-variant">{t("connections.probeEmpty")}</p>
          )}
        </div>
      )}

      {sharedDomains && provider.type === "subscription" && (
        <label className="mb-5 flex cursor-pointer items-start gap-3 rounded-lg bg-surface-container px-3 py-3 text-sm text-on-surface">
          <input
            type="checkbox"
            checked={usesSharedDomains}
            disabled={saving || sharedBusy || (!rule.enabled && !usesSharedDomains)}
            onChange={(event) => onSharedDomainsChange({ ruleId: rule.id, useShared: event.target.checked })}
            className="mt-0.5 h-4 w-4 shrink-0 accent-primary"
          />
          <span>
            <span className="block font-medium">{t("connections.useSharedList")} <span className="font-normal text-on-surface-variant">({sharedDomains.entries?.length ?? 0})</span></span>
            <span className="mt-0.5 block text-xs text-on-surface-variant">{t(usesSharedDomains ? "connections.sharedAndOwn" : "connections.ownOnly")}</span>
          </span>
        </label>
      )}
      <div>
      <h3 className="text-sm font-semibold text-on-surface">{t("connections.ownDomains")} <span className="ml-1 font-normal text-on-surface-variant">({rule.domains.length})</span></h3>
      <p className="mt-2 mb-3 text-xs text-on-surface-variant">{t("connections.ownDomainsHelp")}</p>
      {rule.domains.length > 0 && <ul className="mb-4 max-h-40 space-y-1 overflow-auto" aria-label={t("connections.ownDomains")}>
        {rule.domains.map((domain) => <li key={domain} className="flex items-center justify-between gap-3 border-b border-outline-variant/15 py-1.5 text-sm">
          <span className="break-all font-mono text-xs text-on-surface">{domain}</span>
          <button type="button" onClick={() => removeDomain(domain)} disabled={saving} aria-label={t("connections.removeDomain", { domain })} className="shrink-0 rounded p-1.5 text-on-surface-variant hover:bg-error/10 hover:text-error disabled:opacity-40"><Icon name="close" className="h-4 w-4" /></button>
        </li>)}
      </ul>}
      <form onSubmit={addDomains} className="mb-3 space-y-2">
        <textarea
          aria-label={`${t("connections.ownDomains")}: ${rule.selectedLocation || rule.name}`}
          className="w-full resize-y rounded-lg border-none bg-surface-container-highest px-3 py-2 font-mono text-sm leading-relaxed text-on-surface placeholder:font-mono placeholder:text-on-surface-variant/80 focus:ring-1 focus:ring-primary"
          rows={3}
          value={domainInput}
          onChange={(e) => setDomainInput(e.target.value)}
          placeholder={t("connections.domainsPlaceholder")}
          disabled={saving}
        />
        <div className="flex flex-wrap gap-2">
          <button
            type="submit"
            disabled={saving || !domainInput.trim()}
            className="flex items-center gap-1.5 rounded-lg bg-primary/10 px-4 py-2 text-sm font-medium text-primary transition-colors hover:bg-primary/20 disabled:opacity-40"
          >
            <Icon name="add" className="h-4 w-4" />
            {t("connections.addDomains")}
          </button>
          <button
            type="button"
            disabled={saving}
            onClick={() => fileInputRef.current?.click()}
            className="flex items-center gap-1.5 rounded-lg bg-outline-variant/10 px-4 py-2 text-sm font-medium text-on-surface-variant transition-colors hover:bg-outline-variant/20 disabled:opacity-40"
          >
            <Icon name="upload_file" className="h-4 w-4" />
            {t("connections.importFile")}
          </button>
          <button
            type="button"
            onClick={() => {
              const sample = "# Пример списка доменов для импорта\n# Строки с # игнорируются\n\nyoutube.com\ngooglevideo.com\nyoutu.be\nnetflix.com\nnflxvideo.net\ntwitch.tv\ndiscord.com\ngateway.discord.gg\nspotify.com\naudio-ak-spotify-com.akamaized.net\n";
              const blob = new Blob([ROUTING_ENTRIES_EXAMPLE], { type: "text/plain" });
              const url = URL.createObjectURL(blob);
              const a = document.createElement("a");
              a.href = url;
              a.download = "routing-entries-example.txt";
              a.click();
              URL.revokeObjectURL(url);
            }}
            className="flex items-center gap-1.5 rounded-lg bg-outline-variant/10 px-4 py-2 text-sm font-medium text-outline-variant transition-colors hover:bg-outline-variant/20"
          >
            <Icon name="download" className="h-4 w-4" />
            {t("connections.downloadExample")}
          </button>
          <input ref={fileInputRef} type="file" accept=".txt,.csv,.lst,.list" className="hidden" onChange={handleFileImport} />
        </div>
      </form>

      {rule.domains.length > 0 && (
        <div className="mt-4 border-t border-outline-variant/15 pt-3">
        <button type="button" aria-expanded={showDomainTools} onClick={() => setShowDomainTools(value => !value)} className="rounded-lg border border-outline-variant/20 px-3 py-2 text-xs text-on-surface-variant hover:bg-surface-container-high">{t("connections.domainTools")}</button>
        {showDomainTools && <div className="mt-3">
        <RuleDomainsPanel
          domains={rule.domains}
          saving={saving}
          domainHealthMap={domainHealthMap}
          onRemoveDomain={removeDomain}
          onRemoveDomains={removeDomains}
          onCheckDomains={checkDomainsHealth}
          onExport={() => {
            const blob = new Blob([rule.domains.join("\n") + "\n"], { type: "text/plain" });
            const url = URL.createObjectURL(blob);
            const a = document.createElement("a");
            a.href = url;
            a.download = `${(rule.selectedLocation || rule.name || "domains").replace(/\s+/g, "_")}.txt`;
            a.click();
            URL.revokeObjectURL(url);
          }}
          onClearAll={async () => {
            if (!window.confirm(t("connections.clearAllConfirm"))) return;
            await saveDomains([]);
          }}
          t={t}
        />
        </div>}
        </div>
      )}
      </div>
    </section>
  );
}

function RuleDomainsPanel({ domains, saving, domainHealthMap, onRemoveDomain, onRemoveDomains, onCheckDomains, onExport, onClearAll, t }) {
  const [query, setQuery] = useState("");
  const [healthFilter, setHealthFilter] = useState("all");
  const [selected, setSelected] = useState(() => new Set());
  const deferredQuery = useDeferredValue(query);
  const normalizedQuery = deferredQuery.trim().toLowerCase();

  const filteredDomains = useMemo(() => {
    return domains.filter((domain) => {
      if (normalizedQuery && !domain.toLowerCase().includes(normalizedQuery)) {
        return false;
      }
      return matchesDomainHealthFilter(getDomainHealthRecord(domainHealthMap, domain), healthFilter);
    });
  }, [domainHealthMap, domains, healthFilter, normalizedQuery]);

  const visibleDomains = useMemo(() => {
    return filteredDomains.slice(0, DOMAIN_SEARCH_RESULT_LIMIT);
  }, [filteredDomains]);

  const hiddenCount = Math.max(0, filteredDomains.length - visibleDomains.length);
  const candidateCount = useMemo(() => {
    return filteredDomains.filter((domain) => getDomainHealthRecord(domainHealthMap, domain).decision === "delete_candidate").length;
  }, [domainHealthMap, filteredDomains]);

  const allVisibleSelected = visibleDomains.length > 0 && visibleDomains.every((d) => selected.has(d));

  const toggleOne = useCallback((domain) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(domain)) next.delete(domain);
      else next.add(domain);
      return next;
    });
  }, []);

  const toggleAll = useCallback(() => {
    setSelected((prev) => {
      if (visibleDomains.every((d) => prev.has(d))) {
        const next = new Set(prev);
        for (const d of visibleDomains) next.delete(d);
        return next;
      }
      return new Set([...prev, ...visibleDomains]);
    });
  }, [visibleDomains]);

  const handleDeleteSelected = useCallback(async () => {
    if (selected.size === 0) return;
    const toRemove = [...selected];
    setSelected(new Set());
    await onRemoveDomains(toRemove);
  }, [selected, onRemoveDomains]);

  const handleCheckDomains = useCallback(async () => {
    const targets = selected.size > 0
      ? filteredDomains.filter((domain) => selected.has(domain))
      : filteredDomains;
    if (targets.length === 0) {
      return;
    }
    await onCheckDomains(targets);
  }, [filteredDomains, onCheckDomains, selected]);

  const handleSelectCandidates = useCallback(() => {
    const nextSelection = filteredDomains.filter((domain) => getDomainHealthRecord(domainHealthMap, domain).decision === "delete_candidate");
    setSelected((prev) => new Set([...prev, ...nextSelection]));
  }, [domainHealthMap, filteredDomains]);

  // Clear selection when domains list changes (after delete)
  useEffect(() => {
    setSelected((prev) => {
      const next = new Set([...prev].filter((d) => domains.includes(d)));
      return next.size === prev.size ? prev : next;
    });
  }, [domains]);

  return (
    <>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <label className="relative block flex-1 min-w-[200px]">
          <Icon name="search" className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-outline-variant" />
          <input
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t("connections.domainFilterPlaceholder")}
            className="w-full rounded-lg border-none bg-surface-container-highest py-2 pl-9 pr-3 text-sm text-on-surface placeholder:text-outline-variant focus:ring-1 focus:ring-primary"
            disabled={saving}
          />
        </label>
        <select
          value={healthFilter}
          onChange={(event) => setHealthFilter(event.target.value)}
          disabled={saving}
          className="rounded-lg border-none bg-surface-container-highest px-3 py-2 text-sm text-on-surface focus:ring-1 focus:ring-primary"
        >
          <option value="all">{t("connections.domainHealthFilterAll")}</option>
          <option value="both">{t("connections.domainHealthFilterBoth")}</option>
          <option value="direct_only">{t("connections.domainHealthFilterDirectOnly")}</option>
          <option value="vpn_only">{t("connections.domainHealthFilterVpnOnly")}</option>
          <option value="neither">{t("connections.domainHealthFilterNeither")}</option>
          <option value="candidates">{t("connections.domainHealthFilterCandidates")}</option>
          <option value="unchecked">{t("connections.domainHealthFilterUnchecked")}</option>
        </select>
        <button
          type="button"
          disabled={saving || filteredDomains.length === 0}
          onClick={handleCheckDomains}
          className="flex items-center gap-1.5 rounded-lg bg-primary/10 px-3 py-2 text-xs font-medium text-primary transition-colors hover:bg-primary/20 disabled:opacity-40"
        >
          <Icon name="health_and_safety" className="h-3.5 w-3.5" />
          {t("connections.checkDomains")}
        </button>
        <button
          type="button"
          disabled={saving || candidateCount === 0}
          onClick={handleSelectCandidates}
          className="flex items-center gap-1.5 rounded-lg bg-tertiary/10 px-3 py-2 text-xs font-medium text-tertiary transition-colors hover:bg-tertiary/20 disabled:opacity-40"
        >
          <Icon name="rule" className="h-3.5 w-3.5" />
          {t("connections.selectCandidates", { count: String(candidateCount) })}
        </button>
        {selected.size > 0 && (
          <button
            type="button"
            disabled={saving}
            onClick={handleDeleteSelected}
            className="flex items-center gap-1.5 rounded-lg bg-error/10 px-3 py-2 text-xs font-medium text-error transition-colors hover:bg-error/20 disabled:opacity-40"
          >
            <Icon name="delete" className="h-3.5 w-3.5" />
            {t("connections.deleteSelected", { count: String(selected.size) })}
          </button>
        )}
        <button
          type="button"
          disabled={saving}
          onClick={onExport}
          className="flex items-center gap-1.5 rounded-lg bg-outline-variant/10 px-3 py-2 text-xs font-medium text-on-surface-variant transition-colors hover:bg-outline-variant/20 disabled:opacity-40"
        >
          <Icon name="download" className="h-3.5 w-3.5" />
          {t("connections.exportDomains")}
        </button>
        <button
          type="button"
          disabled={saving}
          onClick={onClearAll}
          className="flex items-center gap-1.5 rounded-lg bg-error/10 px-3 py-2 text-xs font-medium text-error transition-colors hover:bg-error/20 disabled:opacity-40"
        >
          <Icon name="delete_sweep" className="h-3.5 w-3.5" />
          {t("connections.clearAll")}
        </button>
      </div>

      <div className="overflow-hidden rounded-lg border border-outline-variant/15">
        <div className="max-h-[420px] overflow-auto">
          <table className="w-full text-left text-sm">
            <thead className="sticky top-0 z-10 bg-surface-container text-xs uppercase tracking-wider text-outline">
              <tr>
                <th className="w-10 px-3 py-2 text-center">
                  <input
                    type="checkbox"
                    checked={allVisibleSelected}
                    onChange={toggleAll}
                    disabled={saving || visibleDomains.length === 0}
                    className="h-3.5 w-3.5 cursor-pointer accent-primary"
                  />
                </th>
                <th className="w-12 px-3 py-2 text-right">#</th>
                <th className="px-3 py-2">{t("connections.domainColumn")}</th>
                <th className="w-56 px-3 py-2">{t("connections.domainDirectColumn")}</th>
                <th className="w-56 px-3 py-2">{t("connections.domainVpnColumn")}</th>
                <th className="w-36 px-3 py-2">{t("connections.domainDecisionColumn")}</th>
                <th className="w-12 px-3 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y divide-outline-variant/10">
              {visibleDomains.map((domain, idx) => {
                const health = getDomainHealthRecord(domainHealthMap, domain);
                const decisionMeta = getDomainDecisionMeta(health.decision, t);
                const lastCheckedLabel = health.lastCheckedAt
                  ? t("connections.domainHealthChecked", { at: formatDomainHealthTime(health.lastCheckedAt) })
                  : t("connections.domainHealthNever");

                return (
                  <tr key={domain} className={`transition-colors hover:bg-surface-container-highest/60${selected.has(domain) ? " bg-primary/5" : ""}`}>
                    <td className="px-3 py-1.5 text-center">
                      <input
                        type="checkbox"
                        checked={selected.has(domain)}
                        onChange={() => toggleOne(domain)}
                        disabled={saving}
                        className="h-3.5 w-3.5 cursor-pointer accent-primary"
                      />
                    </td>
                    <td className="px-3 py-1.5 text-right font-mono text-xs text-outline-variant">
                      {normalizedQuery || healthFilter !== "all" ? idx + 1 : domains.indexOf(domain) + 1}
                    </td>
                    <td className="px-3 py-1.5">
                      <div className="font-mono text-on-surface">{domain}</div>
                      <div className="mt-0.5 flex flex-wrap items-center gap-2 text-[11px] text-on-surface-variant">
                        <span>{lastCheckedLabel}</span>
                      </div>
                    </td>
                    <td className="px-3 py-1.5 align-top">
                      <DomainPathStatus
                        label={t("connections.domainDirectColumn")}
                        dnsStatus={health.directDnsStatus}
                        transportStatus={health.directTransportStatus}
                        dnsFailures={health.consecutiveDirectDnsFailures}
                        transportFailures={health.consecutiveDirectTransportFailures}
                        lastError={health.directLastError}
                        t={t}
                      />
                    </td>
                    <td className="px-3 py-1.5 align-top">
                      <DomainPathStatus
                        label={t("connections.domainVpnColumn")}
                        dnsStatus={health.vpnDnsStatus}
                        transportStatus={health.vpnTransportStatus}
                        dnsFailures={health.consecutiveVpnDnsFailures}
                        transportFailures={health.consecutiveVpnTransportFailures}
                        lastError={health.vpnLastError}
                        t={t}
                      />
                    </td>
                    <td className="px-3 py-1.5 align-middle">
                      <span className={`inline-flex rounded-full px-2 py-1 text-[10px] font-bold uppercase tracking-wide ${decisionMeta.className}`}>
                        {decisionMeta.label}
                      </span>
                    </td>
                    <td className="px-3 py-1.5 text-center">
                      <button
                        type="button"
                        onClick={() => onRemoveDomain(domain)}
                        disabled={saving}
                        className="text-outline-variant transition-colors hover:text-error disabled:opacity-40"
                      >
                        <Icon name="close" className="h-3.5 w-3.5" />
                      </button>
                    </td>
                  </tr>
                );
              })}
              {visibleDomains.length === 0 && (
                <tr>
                  <td colSpan={7} className="px-4 py-6 text-center text-sm text-on-surface-variant">
                    {t("connections.domainNoMatch")}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <p className="mt-2 text-xs text-on-surface-variant">
        {normalizedQuery || healthFilter !== "all"
          ? hiddenCount > 0
            ? t("connections.domainMatchesPreview", { visible: String(visibleDomains.length), total: String(filteredDomains.length) })
            : t("connections.domainMatches", { total: String(filteredDomains.length) })
          : t("connections.domainTotal", { total: String(domains.length) })}
      </p>
    </>
  );
}

function FormField({ label, hint, children }) {
  return (
    <label className="block">
      <span className="mb-1.5 block font-headline text-xs uppercase tracking-widest text-outline">{label}</span>
      {hint ? <span className="mb-2 block text-[10px] text-outline-variant">{hint}</span> : null}
      {children}
    </label>
  );
}
