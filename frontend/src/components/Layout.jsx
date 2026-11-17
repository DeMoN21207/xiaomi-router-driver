import { useState, useEffect, useRef } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import { fetchJSON } from "../api.js";
import { useI18n } from "../i18n.jsx";
import Icon from "./Icon.jsx";
import GlobalProgress from "./GlobalProgress.jsx";

const navItems = [
  { to: "/", icon: "dashboard", labelKey: "nav.dashboard" },
  { to: "/connections", icon: "vpn_lock", labelKey: "nav.connections" },
  { to: "/traffic", icon: "bar_chart", labelKey: "nav.traffic" },
  { to: "/events", icon: "history", labelKey: "nav.events" },
  { to: "/settings", icon: "settings", labelKey: "nav.settings" },
];

const subtitleKeys = {
  "/": "topbar.management",
  "/connections": "topbar.connections",
  "/traffic": "topbar.traffic",
  "/events": "topbar.events",
  "/settings": "topbar.settings",
};

export default function Layout() {
  const location = useLocation();
  const { t } = useI18n();
  const subtitleKey = subtitleKeys[location.pathname];
  const [bundle, setBundle] = useState(null);
  const [uptimeBase, setUptimeBase] = useState(null);
  const [mobileOpen, setMobileOpen] = useState(false);
  const mobileMenu = useRef(null);

  useEffect(() => { setMobileOpen(false); }, [location.pathname]);

  useEffect(() => {
    const menu = mobileMenu.current;
    if (!mobileOpen) { if (menu?.open) menu.close(); return; }
    menu.showModal();
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const desktop = window.matchMedia("(min-width: 768px)");
    const closeOnDesktop = () => { if (desktop.matches) setMobileOpen(false); };
    desktop.addEventListener("change", closeOnDesktop);
    return () => {
      document.body.style.overflow = previousOverflow;
      desktop.removeEventListener("change", closeOnDesktop);
      if (menu.open) menu.close();
    };
  }, [mobileOpen]);
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem("sidebarCollapsed") === "1";
    } catch {
      return false;
    }
  });

  useEffect(() => {
    try {
      localStorage.setItem("sidebarCollapsed", collapsed ? "1" : "0");
    } catch {
      /* ignore */
    }
  }, [collapsed]);

  useEffect(() => {
    let alive = true;

    async function loadStatus() {
      try {
        const status = await fetchJSON("/api/status");
        if (alive) {
          setBundle(status?.bundle || null);
          const seconds = Number(status?.uptimeSeconds);
          if (Number.isFinite(seconds) && (seconds > 0 || status?.uptimeFormatted)) {
            setUptimeBase({
              seconds: Math.max(0, Math.floor(seconds)),
              receivedAt: Date.now(),
            });
          }
        }
      } catch {
        if (alive) {
          setBundle(null);
        }
      }
    }

    loadStatus();
    const id = window.setInterval(loadStatus, 60_000);
    return () => {
      alive = false;
      window.clearInterval(id);
    };
  }, []);

  const sidebarWidth = collapsed ? "w-16" : "w-64";
  const contentOffset = collapsed ? "md:ml-16" : "md:ml-64";
  const topbarOffset = collapsed ? "md:left-16" : "md:left-64";

  return (
    <div className="min-h-screen bg-surface text-on-surface">
      <GlobalProgress />
      {/* Sidebar */}
      <aside className={`hidden md:flex h-screen ${sidebarWidth} fixed left-0 top-0 z-40 bg-surface-container-low shadow-2xl shadow-black/40 flex-col pt-20 pb-6 ${collapsed ? "px-2" : "px-4"} transition-[width] duration-200`}>
        {/* Collapse toggle — vertically centered on the right edge */}
        <button
          type="button"
          onClick={() => setCollapsed((v) => !v)}
          title={collapsed ? t("nav.expand") : t("nav.collapse")}
          className="absolute top-1/2 -right-3 -translate-y-1/2 z-50 flex h-7 w-7 items-center justify-center rounded-full border border-outline-variant/30 bg-surface-container-high text-on-surface-variant shadow-lg transition-colors hover:border-primary/40 hover:text-primary"
        >
          <Icon name={collapsed ? "chevron_right" : "chevron_left"} className="h-4 w-4" />
        </button>

        <div className={`mb-8 ${collapsed ? "px-0 text-center" : "px-4"}`}>
          {collapsed ? (
            <div className="mx-auto flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15 font-headline text-sm font-black text-primary">
              RV
            </div>
          ) : (
            <div>
              <h1 className="font-headline font-black text-primary text-lg tracking-tight">
                RouteVPN Manager
              </h1>
              <BundleVersionBadge bundle={bundle} t={t} className="mt-2 max-w-full" />
            </div>
          )}
        </div>

        <NavigationLinks collapsed={collapsed} t={t} />

        <SidebarClock collapsed={collapsed} uptimeBase={uptimeBase} t={t} />
      </aside>

      <dialog
        ref={mobileMenu}
        id="mobile-navigation"
        aria-label={t("nav.navigation")}
        onClose={event => { if (!event.currentTarget.open) setMobileOpen(false); }}
        onCancel={() => setMobileOpen(false)}
        onKeyDown={event => {
          if (event.key !== "Tab") return;
          const controls = event.currentTarget.querySelectorAll("a[href], button:not([disabled])");
          const first = controls[0], last = controls[controls.length - 1];
          if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
          else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
        }}
        onClick={event => {
          if (event.target !== event.currentTarget) return;
          const rect = event.currentTarget.getBoundingClientRect();
          if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) setMobileOpen(false);
        }}
        className="fixed inset-y-0 left-0 right-auto m-0 h-dvh max-h-none w-[min(20rem,85vw)] max-w-none overflow-y-auto border-r border-outline-variant/20 bg-surface-container-low p-4 text-on-surface shadow-2xl backdrop:bg-black/60"
      >
        <div className="mb-6 flex items-center justify-between gap-2">
          <span className="font-headline font-bold text-primary">RouteVPN Manager</span>
          <button type="button" autoFocus onClick={() => setMobileOpen(false)} aria-label={t("nav.close")} className="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg hover:bg-surface-container-high focus-visible:outline-2 focus-visible:outline-primary">
            <Icon name="close" className="h-6 w-6" />
          </button>
        </div>
        <NavigationLinks t={t} onNavigate={() => setMobileOpen(false)} />
        <BundleVersionBadge bundle={bundle} t={t} className="mt-6 max-w-full" />
      </dialog>

      {/* Top Bar */}
      <header className={`fixed top-0 right-0 left-0 ${topbarOffset} z-50 bg-surface flex justify-between items-center gap-3 px-4 sm:px-6 md:px-8 py-3 h-16 border-b border-outline-variant/10 transition-[left] duration-200`}>
        <div className="flex min-w-0 items-center gap-3">
          <button type="button" aria-label={t("nav.open")} aria-controls="mobile-navigation" aria-expanded={mobileOpen} onClick={() => setMobileOpen(true)} className="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg text-primary hover:bg-surface-container-high focus-visible:outline-2 focus-visible:outline-primary md:hidden">
            <Icon name="menu" className="h-6 w-6" />
          </button>
          {subtitleKey ? (
            <span className="truncate font-headline text-sm sm:text-base lg:text-xl font-bold text-primary tracking-wide uppercase">
              {t(subtitleKey)}
            </span>
          ) : (
            <span className="font-headline text-xl font-bold text-primary tracking-widest">
              RouteVPN
            </span>
          )}
        </div>

        <BundleVersionBadge bundle={bundle} t={t} className="!hidden sm:!inline-flex max-w-[30vw] md:max-w-xs shrink-0" />

      </header>

      {/* Main Content */}
      <main className={`${contentOffset} pt-20 sm:pt-24 pb-12 px-4 sm:px-6 md:px-8 min-w-0 min-h-screen transition-[margin] duration-200`}>
        <div className="min-w-0 max-w-7xl mx-auto">
          <Outlet />
        </div>
      </main>
    </div>
  );
}

function NavigationLinks({ collapsed = false, t, onNavigate }) {
  return <nav aria-label={t("nav.navigation")} className="flex-1 space-y-1">
    {navItems.map(item => <NavLink key={item.to} to={item.to} end={item.to === "/"} onClick={onNavigate} title={collapsed ? t(item.labelKey) : undefined}
      className={({isActive}) => `flex min-h-11 items-center gap-3 rounded-lg ${collapsed ? "justify-center px-2" : "px-4"} py-3 font-headline text-sm transition-colors focus-visible:outline-2 focus-visible:outline-primary ${isActive ? "bg-primary/10 text-primary" : "text-on-surface-variant hover:bg-surface-container-high hover:text-on-surface"}`}>
      <Icon name={item.icon} className="h-5 w-5 shrink-0" />
      {!collapsed && t(item.labelKey)}
    </NavLink>)}
  </nav>;
}

function BundleVersionBadge({ bundle, t, className = "" }) {
  const label = formatBundleLabel(bundle, t);
  const target = [bundle?.goos, bundle?.goarch].filter(Boolean).join("/");
  const title = [
    `${t("build.version")}: ${label}`,
    target ? `${t("build.target")}: ${target}` : "",
    bundle?.builtAt ? `${t("build.builtAt")}: ${bundle.builtAt}` : "",
  ].filter(Boolean).join(" · ");

  return (
    <span
      title={title}
      className={`inline-flex min-w-0 items-center gap-1.5 rounded-full border border-outline-variant/20 bg-surface-container-high px-2.5 py-1 font-mono text-[10px] font-semibold text-on-surface-variant ${className}`}
    >
      <Icon name="terminal" className="h-3.5 w-3.5 shrink-0 text-secondary" />
      <span className="truncate">{label}</span>
    </span>
  );
}

function formatBundleLabel(bundle, t) {
  if (!bundle) {
    return t("build.unknown");
  }
  const value = bundle.version || bundle.commit || [bundle.goos, bundle.goarch].filter(Boolean).join("/");
  return value || t("build.unknown");
}

function SidebarClock({ collapsed, uptimeBase, t }) {
  const [now, setNow] = useState(() => new Date());

  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(id);
  }, []);

  if (collapsed) return null;

  const uptimeSeconds = uptimeBase
    ? uptimeBase.seconds + Math.max(0, Math.floor((now.getTime() - uptimeBase.receivedAt) / 1000))
    : null;
  const uptimeLabel = uptimeSeconds === null ? "--:--:--" : formatUptimeClock(uptimeSeconds);

  const date = now.toLocaleDateString("ru-RU", { day: "2-digit", month: "2-digit", year: "numeric" });
  const time = now.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit", second: "2-digit" });

  const dayOfWeek = now.toLocaleDateString("ru-RU", { weekday: "long" });

  return (
    <div className="border-t border-outline-variant/10 px-4 pt-4 space-y-1">
      <div className="font-mono text-sm font-semibold text-on-surface tracking-tight">{time}</div>
      <div className="text-xs text-on-surface-variant">{date}, {dayOfWeek}</div>
      <div className="flex items-center gap-1.5 pt-1" title={t("layout.uptimeTitle")}>
        <Icon name="timer" className="h-3.5 w-3.5 text-primary/70" />
        <span className="text-[10px] font-bold uppercase tracking-wider text-on-surface-variant">{t("layout.uptime")}</span>
        <span className="ml-auto font-mono text-xs text-primary/70">{uptimeLabel}</span>
      </div>
    </div>
  );
}

function formatUptimeClock(totalSeconds) {
  const safeSeconds = Math.max(0, Math.floor(totalSeconds));
  const days = Math.floor(safeSeconds / 86400);
  const h = String(Math.floor((safeSeconds % 86400) / 3600)).padStart(2, "0");
  const m = String(Math.floor((safeSeconds % 3600) / 60)).padStart(2, "0");
  const s = String(safeSeconds % 60).padStart(2, "0");
  const clock = `${h}:${m}:${s}`;
  return days > 0 ? `${days}д ${clock}` : clock;
}
