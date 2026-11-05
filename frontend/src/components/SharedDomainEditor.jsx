import { useDeferredValue, useState } from "react";
import { parseDomainInput } from "../domainInput.js";
import Icon from "./Icon.jsx";

export default function SharedDomainEditor({ list, busy, onSave, t }) {
  const [draft, setDraft] = useState(null);
  const [query, setQuery] = useState("");
  const entries = list.entries || [];
  const search = useDeferredValue(query.trim().toLowerCase());
  const matches = entries.filter((entry) => entry.toLowerCase().includes(search));

  function exportList() {
    const url = URL.createObjectURL(new Blob([entries.join("\n") + "\n"], { type: "text/plain" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = "shared-domains.txt";
    link.click();
    URL.revokeObjectURL(url);
  }

  return (
    <section aria-labelledby="shared-domain-editor" className="rounded-xl border border-outline-variant/20 bg-surface-container-low p-4 sm:p-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0 flex-1">
          <h2 id="shared-domain-editor" className="flex items-center gap-2 font-headline text-lg font-bold text-on-surface">
            {t("connections.sharedList")}
            <span className="rounded bg-surface-container-high px-2 py-0.5 font-body text-xs font-medium text-on-surface-variant">{entries.length}</span>
          </h2>
          <p className="mt-1 max-w-2xl text-sm text-on-surface-variant">{t("connections.sharedListHelp")}</p>
        </div>
        {draft === null && (
          <div className="flex shrink-0 items-center gap-2">
            <button type="button" onClick={exportList} disabled={!entries.length} className="rounded-lg px-3 py-2 text-sm text-on-surface-variant transition-colors hover:bg-surface-container-high disabled:opacity-40">
              {t("connections.exportDomains")}
            </button>
            <button type="button" onClick={() => setDraft(entries.join("\n"))} disabled={busy} className="flex items-center gap-2 rounded-lg bg-primary/10 px-3 py-2 text-sm font-medium text-primary transition-colors hover:bg-primary/20 disabled:opacity-40">
              <Icon name="edit" className="h-4 w-4" />
              {t("connections.editSharedList")}
            </button>
          </div>
        )}
      </div>
      {draft === null ? (
        <div className="mt-4">
          {entries.length > 0 ? (
            <>
              <input type="search" aria-label={t("connections.sharedDomainsSearch")} placeholder={t("connections.sharedDomainsSearch")} value={query} onChange={(event) => setQuery(event.target.value)} className="mb-3 w-full rounded-lg border border-outline-variant/30 bg-surface-container px-3 py-2 text-sm text-on-surface placeholder:text-on-surface-variant focus:ring-1 focus:ring-primary sm:max-w-xs" />
              {matches.length ? (
                <ul aria-label={t("connections.sharedList")} className="grid max-h-48 grid-cols-1 gap-x-6 overflow-auto sm:grid-cols-2 lg:grid-cols-3">
                  {matches.map((entry) => <li key={entry} className="break-all border-b border-outline-variant/10 py-2 font-mono text-xs text-on-surface">{entry}</li>)}
                </ul>
              ) : <p role="status" className="py-3 text-sm text-on-surface-variant">{t("connections.sharedDomainsNoMatches")}</p>}
            </>
          ) : <p className="text-sm text-on-surface-variant">{t("connections.sharedListEmpty")}</p>}
        </div>
      ) : (
        <form className="mt-4 space-y-3" onSubmit={async (event) => {
          event.preventDefault();
          if (await onSave({ entries: parseDomainInput(draft) })) setDraft(null);
        }}>
          <label className="block text-sm text-on-surface-variant" htmlFor="shared-domain-draft">{t("connections.domainListInputHelp")}</label>
          <textarea id="shared-domain-draft" aria-label={t("connections.sharedList")} placeholder={t("connections.domainsPlaceholder")} className="w-full resize-y rounded-lg border border-outline-variant/30 bg-surface-container px-3 py-2 font-mono text-sm text-on-surface focus:ring-1 focus:ring-primary" rows={8} value={draft} onChange={(event) => setDraft(event.target.value)} disabled={busy} />
          <div className="flex flex-wrap items-center gap-2">
            <button type="submit" disabled={busy} className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-on-primary disabled:opacity-40">{busy ? t("common.loading") : t("connections.prioritySave")}</button>
            <button type="button" disabled={busy} onClick={() => setDraft(null)} className="rounded-lg px-3 py-2 text-sm text-on-surface-variant hover:bg-surface-container-high disabled:opacity-40">{t("connections.cancel")}</button>
          </div>
        </form>
      )}
    </section>
  );
}
