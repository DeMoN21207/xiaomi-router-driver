# Design System

## Existing Visual Language

This router dashboard supports light and dark themes. The user's screenshots use the dark theme; this change follows the existing theme rather than selecting a new palette.

## Tokens

Reuse semantic Tailwind tokens from `frontend/src/styles.css`: `surface-container-high`, `surface-container-highest`, `on-surface`, `on-surface-variant`, `primary`, `secondary`, `tertiary`, `error` and `outline-variant`. Do not introduce new decorative colors.

## Typography and Layout

Existing Inter body text, Space Grotesk headings and JetBrains Mono numeric readings remain. Server choices wrap naturally in their existing policy card. Each list retains its own controls and state.

## Interaction

Server buttons use restrained borders and background tints. Hover and keyboard focus reveal interactivity; active selection reflects confirmed runtime state. Pending changes show an inline spinning sync icon and text, and prevent conflicting clicks. Errors appear in the affected list with `role="alert"`.
