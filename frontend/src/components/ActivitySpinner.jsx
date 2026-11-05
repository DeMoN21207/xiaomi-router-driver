export default function ActivitySpinner({ className = "" }) {
  return <svg aria-hidden="true" className={`activity-spinner ${className}`} viewBox="0 0 44 44" fill="none">
    <circle cx="22" cy="22" r="18" stroke="currentColor" strokeWidth="3" opacity="0.16" />
    <circle cx="22" cy="22" r="18" stroke="currentColor" strokeWidth="3" strokeLinecap="round" pathLength="1" strokeDasharray="0.24 0.76" />
  </svg>;
}
