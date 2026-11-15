// Una sola acción viva por carril: empezar otra cancela las peticiones de la anterior y la deja sin efecto, así una
// respuesta que llega tarde (o desordenada) nunca publica nada encima de lo que el usuario pidió después.

export interface Accion {
  /** Para pasar a fetch/api: se aborta al empezar otra acción o al cancelar el carril. */
  signal: AbortSignal;
  /** true mientras sea la última acción del carril y no se haya cancelado. Comprobar antes de cada publicación. */
  vigente(): boolean;
}

export class Acciones {
  private gen = 0;
  private ctrl?: AbortController;

  begin(): Accion {
    this.ctrl?.abort();
    const ctrl = new AbortController();
    this.ctrl = ctrl;
    const mine = ++this.gen;
    return { signal: ctrl.signal, vigente: () => mine === this.gen && !ctrl.signal.aborted };
  }

  /** Al salir: nada pendiente se aplica y sus peticiones se cortan. */
  cancel() {
    this.ctrl?.abort();
    this.ctrl = undefined;
    this.gen++;
  }
}
