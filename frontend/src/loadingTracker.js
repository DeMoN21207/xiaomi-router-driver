let nextID = 0;
const operations = new Map(), listeners = new Set();

function emit() {
  const snapshot = [...operations.values()];
  for (const listener of listeners) {
    try { listener(snapshot.length, snapshot); } catch { /* Keep request cleanup independent of UI listeners. */ }
  }
}

export function subscribeLoading(listener) {
  listeners.add(listener);
  listener(operations.size, [...operations.values()]);
  return () => listeners.delete(listener);
}

export function beginLoading(labelKey = "loading.processing") {
  const id = ++nextID;
  operations.set(id, { id, labelKey, startedAt: Date.now() });
  emit();
  return () => { if (operations.delete(id)) emit(); };
}
