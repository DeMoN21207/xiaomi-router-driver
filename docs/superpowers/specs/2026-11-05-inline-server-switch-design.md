# Inline server switching

The user explicitly wants to click a server name in the existing automatic-routing list and switch immediately. Preserve the layout in their screenshot and the backend behavior already verified: manual choice wins while healthy, failure clears the override and resumes automatic selection.

## Approach

Make the existing server chips native buttons calling the existing priority override endpoint. A separate selector already exists on the overview, but the user explicitly chose direct interaction in this list. No confirmation modal or new routing mode is needed.

## Behavior

- Apply only the clicked policy, including the synthetic shared-list policy.
- Keep the current active marker until refreshed backend status confirms a change.
- Show pending feedback on the chosen server, prevent concurrent requests, and disable controls for disabled policies.
- Offer “Вернуть автовыбор” next to a manual override.
- On failure, preserve the working route and show the error in the affected card.
- Keep DNS, MEGA's new prefix, domains, ten candidates and DeepSeek ownership intact during deployment.

## Validation

Use a local preview with controlled network responses to check click, pending, rejection, independent policy selection, synthetic shared policy and return to automatic selection. Run frontend tests/build and existing backend manual-fallback tests. Deploy with rollback, verify the served UI and preserved settings, and publish to main under the user's existing authorization.
