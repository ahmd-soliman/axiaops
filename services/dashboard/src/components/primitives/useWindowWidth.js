import { useSyncExternalStore } from 'react';

// Single shared window-width store. Every useWindowWidth/useBreakpoint
// call-site used to attach its own `resize` listener and call setWidth on
// each event; with ~12 concurrent consumers a window drag produced ~12 ×
// 60Hz state updates and re-renders across the tree. Here one module-level
// listener (attached lazily while at least one component is subscribed)
// coalesces a burst of resize events into a single innerWidth read per
// animation frame and fans the result out via useSyncExternalStore, so all
// consumers share one DOM read and only re-render when the width actually
// changes.

let currentWidth = typeof window !== 'undefined' ? window.innerWidth : 0;
const listeners = new Set();
let rafId = null;
let attached = false;

function flush() {
  rafId = null;
  const next = window.innerWidth;
  if (next === currentWidth) return;
  currentWidth = next;
  for (const listener of listeners) listener();
}

function onResize() {
  if (rafId !== null) return; // already scheduled for this frame
  rafId = requestAnimationFrame(flush);
}

function subscribe(listener) {
  listeners.add(listener);
  if (!attached) {
    window.addEventListener('resize', onResize);
    attached = true;
    // Re-sync in case the width changed between module load and first mount.
    currentWidth = window.innerWidth;
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0 && attached) {
      window.removeEventListener('resize', onResize);
      attached = false;
      if (rafId !== null) {
        cancelAnimationFrame(rafId);
        rafId = null;
      }
    }
  };
}

function getSnapshot() {
  return currentWidth;
}

export function useWindowWidth() {
  // getSnapshot doubles as the server snapshot — the dashboard is a
  // client-only SPA, but passing it keeps useSyncExternalStore happy.
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}
