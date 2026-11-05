// getScreenCTM accounts for preserveAspectRatio letterboxing, viewBox origins,
// and CSS transforms. Distances remain in CSS pixels at every viewport size.
export function findChartHit(points, matrix, clientX, clientY, mode = 'proximity', threshold = 24) {
  if (!matrix) return null;
  let closest = null;
  let distance = Infinity;
  for (const point of points) {
    for (const hit of point.hits?.length ? point.hits : [point]) {
      const x = matrix.a * hit.x + matrix.c * hit.y + matrix.e;
      const y = matrix.b * hit.x + matrix.d * hit.y + matrix.f;
      const next = mode === 'x' || mode === 'bucket'
        ? Math.abs(x - clientX)
        : Math.hypot(x - clientX, y - clientY);
      if (next < distance) {
        distance = next;
        closest = { point, x, y };
      }
    }
  }
  return mode === 'x' || mode === 'bucket' || distance < threshold ? closest : null;
}

export function tooltipLeft(x, parentWidth, tooltipWidth) {
  const preferred = x > parentWidth / 2 ? x - tooltipWidth - 12 : x + 12;
  return Math.max(0, Math.min(preferred, parentWidth - tooltipWidth));
}

export function trafficPointLabel(value, range, locale, timeZone) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const options = range === '30d'
    ? { day: '2-digit', month: 'short' }
    : range === '1h' || range === '3h'
      ? { hour: '2-digit', minute: '2-digit' }
      : { day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit' };
  return new Intl.DateTimeFormat(locale, { ...options, ...(timeZone ? { timeZone } : {}) }).format(date);
}

export function averageHistoryRate(history) {
  const seconds = (Date.parse(history?.to) - Date.parse(history?.from)) / 1000;
  if (!history?.latestSampleAt || !Number.isFinite(seconds) || seconds <= 0) return null;
  const total = Number(history.totalBytes);
  return Number.isFinite(total) && total >= 0 ? total / seconds : null;
}
