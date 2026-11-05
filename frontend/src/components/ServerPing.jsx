import { formatPriorityPing, priorityPing } from "../priorityPing.js";

export default function ServerPing({ target, t }) {
  const ping = priorityPing(target);
  return <span title={t(ping.state === "unavailable" ? "connections.pingNoResponseHelp" : "connections.serverPingHelp")} className={`whitespace-nowrap font-mono text-xs ${ping.state === "ready" ? "text-on-surface" : "text-on-surface-variant"}`}>{formatPriorityPing(target, t)}</span>;
}
