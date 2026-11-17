# Simple VPN controls

User request: see current state, add/cancel and switch manually without navigating an unintuitive routing interface.

Implementation choice: one main screen with independent site lists. Each list shows the actual active server, always-visible server selector (automatic or a specific configured candidate), and site preview. Server selection is a draft until Switch; Cancel restores the existing selection. Sites open inline, support add/remove, and are saved only with Save. Advanced routing and subscription management stay accessible as secondary tabs.

Alternatives considered: only renaming existing controls would retain excessive navigation; a wizard would add steps to everyday switching. Inline controls match the requested workflow.

Preserve shared versus rule-owned domains and existing failover semantics. Never copy effective runtime domains into saved rules. Failed saves retain drafts. Empty lists remain editable. Polling must not replace drafts or falsely report a configured server as active. Reuse existing theme, APIs, React and native controls, touch targets >=44px and 320px layout. No new dependencies or backend configuration migrations.

Authorization: the user requested implementation and previously authorized router installation. Design choices are routine implementation decisions, not an additional approval gate.
