import { useId } from "react";

export default function SectionTabs({ items, value, onChange, label, idPrefix }) {
  const generatedId = useId();
  const id = idPrefix || generatedId;
  return <div role="tablist" aria-label={label} className="flex gap-1 overflow-x-auto border-b border-outline-variant/20">
    {items.map((item, index) => <button key={item.id} id={`${id}-tab-${item.id}`} type="button" role="tab" aria-controls={`${id}-panel-${item.id}`} aria-selected={value === item.id} tabIndex={value === item.id ? 0 : -1} onClick={() => onChange(item.id)} onKeyDown={event => {
      const next = event.key === "ArrowRight" ? (index + 1) % items.length : event.key === "ArrowLeft" ? (index - 1 + items.length) % items.length : event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : -1;
      if (next < 0) return;
      event.preventDefault();
      onChange(items[next].id);
      event.currentTarget.parentElement.querySelectorAll('[role="tab"]')[next].focus();
    }} className={`shrink-0 border-b-2 px-3 py-3 text-sm font-medium transition-colors sm:px-4 ${value === item.id ? "border-primary text-primary" : "border-transparent text-on-surface-variant hover:text-on-surface"}`}><span className={item.shortLabel ? "hidden sm:inline" : ""}>{item.label}</span>{item.shortLabel && <span className="sm:hidden">{item.shortLabel}</span>}</button>)}
  </div>;
}
