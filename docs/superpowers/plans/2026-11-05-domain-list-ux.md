# Domain lists and subscription interface

**Goal:** One shared list, with own domains and an include-shared checkbox for each server. Keep separate DeepSeek routing and existing failover working.

**Design:** A calm subscription dashboard using the existing light/dark themes. Show subscription state and current servers first. Put credentials, adding servers, diagnostics, and priority editing behind explicit disclosures. Avoid a second all-domains table and technical status text in the main workflow.

**Confirmed:** Own domains and shared-list selection belong to each server, per the user's November 5 answer. Shared-list membership must preserve and include the server's own saved list.

- [x] Replace duplicated domain tables with one searchable shared list and inline editor.
- [x] Add configuration and failover regression tests for union, deduplication, custom-only routes, and immutable saved lists.
- [x] Merge shared entries into the primary route; retain own entries on other member routes.
- [x] Refine subscription headers, per-server domain controls, and advanced disclosures.
- [x] Verify removing the final custom entry preserves the server and shared selection.
- [x] Verify desktop/mobile layouts and shared/custom checkbox interactions using synthetic data.
- [x] Run Go race tests, frontend tests/build, and independent review.
- [ ] Integrate into main and deploy the verified application with rollback protection; check actual Wi-Fi routes.

**Review focus:** Existing independent policies; custom entries on reserve servers; empty custom lists; duplicate shared/custom entries; failed saves retaining the draft and saved configuration.
