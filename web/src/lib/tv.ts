// La app de TV se identifica en el User-Agent. Se lee antes de pintar: la composición no puede saltar tras cargar la página.
export const isTv = /\bVeloraTV\b/.test(navigator.userAgent);

declare global {
  interface Window {
    VeloraApp?: { escanearVinculacion?: () => void; reproducir?: (datos: string) => boolean; mantenerPantalla?: (encendida: boolean) => void; pantallaCompletaReproductor?: (activa: boolean) => void };
  }
}

if (isTv) document.documentElement.classList.add('tv');
