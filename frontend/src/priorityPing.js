export function priorityPing(target, now = Date.now()) {
  const checked = Date.parse(target?.pingCheckedAt || "");
  if (!Number.isFinite(checked)) return { state: "unknown" };
  const age = now - checked;
  if (age < -5_000 || age > 120_000) return { state: "stale" };
  if (target.pingError) return { state: "unavailable" };
  return Number.isFinite(target.pingMs) && target.pingMs > 0 ? { state: "ready", ms: target.pingMs } : { state: "unknown" };
}

export function formatPriorityPing(target, t, now = Date.now()) {
  const ping = priorityPing(target, now);
  if (ping.state === "ready") return `${ping.ms} ${t("common.ms")}`;
  return t(ping.state === "stale" ? "connections.pingStale" : ping.state === "unavailable" ? "connections.pingNoResponse" : "connections.pingUnknown");
}
