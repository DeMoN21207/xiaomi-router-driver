export function eventMessage(message) {
  return String(message ?? "").replace(/\u001b\[[0-?]*[ -/]*[@-~]/g, "");
}
