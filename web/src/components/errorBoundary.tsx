// Red de seguridad de las pantallas: si una pantalla falla al dibujarse, React desmontaría toda la aplicación y quedaría
// solo el fondo oscuro (lo que pasaba en la TV con alguna ficha). En su lugar se muestra un aviso con «Reintentar» e «Ir
// al inicio», con el texto del error en pequeño para poder diagnosticarlo. Al cambiar de pantalla se vuelve a intentar.
import { Component, type ErrorInfo, type ReactNode } from 'react';

interface Props {
  resetKey: string;
  children: ReactNode;
}
interface State {
  error: Error | null;
  key: string;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, key: this.props.resetKey };

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error };
  }

  static getDerivedStateFromProps(props: Props, state: State): Partial<State> | null {
    return props.resetKey !== state.key ? { error: null, key: props.resetKey } : null;
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Pantalla con error', error, info.componentStack);
  }

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    const where = (error.stack ?? '').split('\n').find((l) => l.includes('at ') && !l.includes('react-dom')) ?? '';
    return (
      <main role="alert" className="flex min-h-[60vh] flex-col items-center justify-center gap-4 p-6 text-center">
        <h1 className="text-xl font-semibold text-text">Esta pantalla no se ha podido mostrar</h1>
        <p className="max-w-md text-sm text-text-muted">Ha ocurrido un error inesperado. Puedes volver a intentarlo o ir al inicio.</p>
        <div className="flex gap-3">
          <button
            type="button"
            autoFocus
            onClick={() => this.setState({ error: null })}
            className="h-10 rounded-md bg-accent px-4 text-sm font-semibold text-on-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
          >
            Reintentar
          </button>
          <button
            type="button"
            onClick={() => window.location.assign('/')}
            className="h-10 rounded-md bg-surface-raised px-4 text-sm font-semibold text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
          >
            Ir al inicio
          </button>
        </div>
        <p className="max-w-2xl break-words font-mono text-xs text-text-subtle">
          {error.name}: {error.message}
          {where && <><br />{where.trim()}</>}
        </p>
      </main>
    );
  }
}
