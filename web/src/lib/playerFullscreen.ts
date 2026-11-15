import { useEffect } from 'react';
import { isTv } from './tv';

/** Enter before media preparation spends the activation from the Play tap.
 * Android's immersive bridge also works when opening a saved player URL. */
export function useMobilePlayerFullscreen(enabled: boolean) {
  useEffect(() => {
    if (!enabled || isTv) return;
    const app = window.VeloraApp;
    if (app?.pantallaCompletaReproductor) {
      app.pantallaCompletaReproductor(true);
      return () => app.pantallaCompletaReproductor?.(false);
    }
    if (!window.matchMedia('(pointer: coarse)').matches && !/Android|iPhone|iPad|Mobile/i.test(navigator.userAgent)) return;
    let alive = true;
    let pending = true;
    let requesting = false;
    let owned = false;
    const root = document.documentElement;
    const enter = () => {
      if (!pending || requesting || document.fullscreenElement || !root.requestFullscreen) return;
      requesting = true;
      void root.requestFullscreen({ navigationUI: 'hide' }).then(() => {
        owned = true;
        pending = false;
        if (!alive && document.fullscreenElement === root) void document.exitFullscreen().catch(() => undefined);
      }).catch(() => { /* A direct URL may require the first tap instead. */ })
        .finally(() => { requesting = false; });
    };
    enter();
    document.addEventListener('pointerdown', enter, true);
    return () => {
      alive = false;
      document.removeEventListener('pointerdown', enter, true);
      if (owned && document.fullscreenElement === root) void document.exitFullscreen().catch(() => undefined);
    };
  }, [enabled]);
}
