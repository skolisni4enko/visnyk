// Shared Wails helpers — deduplicates 12x isWailsAvailable checks
export function isWailsAvailable() {
  return !!(window.go && window.go.ui && window.go.ui.App);
}

export async function callWails(fn, ...args) {
  if (!isWailsAvailable()) throw new Error('Wails not available: run via wails dev');
  return fn(...args);
}

// Safe wrapper that returns [result, error] without throwing
export async function tryWails(fn, ...args) {
  try {
    const res = await callWails(fn, ...args);
    return [res, null];
  } catch (e) {
    return [null, String(e)];
  }
}
