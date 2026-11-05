# Routing workspace

The user clarified that active routing is the collection of servers currently used for automatic routing. The first screen must explain the current scheme and permit server switching. Configuration and subscriptions must be distinct sections; native disclosures and controls scattered across nested cards are rejected.

Use three sections: **Сейчас работает**, **Настройка маршрутов**, **Подписки и серверы**. The default section shows one current scheme with destination lists, actual selected servers, manual/automatic mode, and direct switching controls. The routing section separates domain assignment from automatic selection and fallback order. The catalogue contains subscription credentials, discovery, latency checks, and selecting a server for configuration.

Retain the existing backend, shared/custom union, independent DeepSeek policy, and saved settings. Switching a server for an enabled priority policy uses the existing override API; returning to automatic selection clears that override. Fixed routes use their existing rule update endpoint. Display actual runtime decisions where known and distinguish unavailable status from configured locations. Do not add multiple saved schemes.

Use the existing light/dark tokens with restrained borders, consistent panel headers and buttons, and responsive stacking. Replace details/summary with explicit navigation or labelled buttons. Forms show actions below their fields. All sections and switching controls must work with the keyboard.

Approach: reorganize the existing editors behind explicit sections and add a focused current-routing component. A single page of expanded cards continues mixing unrelated tasks; multiple new backend profiles solve a different problem. Implementation choices remain local to this frontend change.
