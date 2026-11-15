// Desplegable propio de Velora en toda la web (ordenador, móvil y TV), en lugar del desplegable del sistema: un botón que
// abre una lista en una ventana modal (role="menu", como el menú de las carátulas). Al elegir una opción se aplica y la
// ventana se cierra; Escape, Atrás o un clic fuera la cierran sin cambiar nada. En la TV, además, un <select> atrapaba
// las flechas arriba/abajo del mando (navegacion.js del APK) y no dejaba bajar hasta el reparto.
import { Children, isValidElement, useEffect, useRef, useState, type ChangeEvent, type ReactElement, type ReactNode, type SelectHTMLAttributes } from 'react';

type Props = SelectHTMLAttributes<HTMLSelectElement> & { children: ReactNode };

interface Option {
  value: string;
  label: string;
  disabled: boolean;
}

function text(node: ReactNode): string {
  return Children.toArray(node)
    .map((n) => (typeof n === 'string' || typeof n === 'number' ? String(n) : isValidElement<{ children?: ReactNode }>(n) ? text(n.props.children) : ''))
    .join('');
}

function options(children: ReactNode): Option[] {
  return Children.toArray(children)
    .filter((c) => isValidElement<{ value?: unknown; disabled?: boolean; children?: ReactNode }>(c))
    .map((c) => {
      const p = (c as ReactElement<{ value?: unknown; disabled?: boolean; children?: ReactNode }>).props;
      return { value: String(p.value ?? ''), label: text(p.children), disabled: !!p.disabled };
    });
}

export function Choice(props: Props) {
  return <ModalChoice {...props} />;
}

function ModalChoice({ children, value, onChange, disabled, className, 'aria-label': ariaLabel }: Props) {
  const [open, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const opts = options(children);
  const current = String(value ?? '');
  const label = opts.find((o) => o.value === current)?.label ?? '';

  const close = () => {
    setOpen(false);
    setTimeout(() => button.current?.focus(), 0);
  };
  const closeRef = useRef(close);
  closeRef.current = close;

  // Mientras está abierto, el foco va a la opción actual y se queda dentro. En el WebView de la TV, enfocar justo al abrir
  // no siempre entra: se vuelve a intentar, y cualquier flecha u OK con el foco fuera lo lleva a la opción actual. Atrás o
  // Escape cierran desde donde esté el foco (el Atrás de la app manda Escape al elemento enfocado, que podía ser el body
  // y no llegaba al menú: el modal parecía bloqueado).
  useEffect(() => {
    if (!open) return;
    const inside = () => !!list.current?.contains(document.activeElement);
    const focusCurrent = () => {
      const items = [...(list.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not([disabled])') ?? [])];
      (items.find((b) => b.dataset.value === current) ?? items[0])?.focus();
    };
    const t1 = setTimeout(focusCurrent, 0);
    const t2 = setTimeout(() => !inside() && focusCurrent(), 150);
    const t3 = setTimeout(() => !inside() && focusCurrent(), 500);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' || e.key === 'GoBack' || (e.key === 'Backspace' && !inside())) {
        e.preventDefault();
        e.stopPropagation();
        closeRef.current();
      } else if (!inside() && ['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Enter', ' '].includes(e.key)) {
        e.preventDefault();
        e.stopPropagation();
        focusCurrent();
      }
    };
    document.addEventListener('keydown', onKey, true);
    return () => {
      clearTimeout(t1);
      clearTimeout(t2);
      clearTimeout(t3);
      document.removeEventListener('keydown', onKey, true);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  const choose = (v: string) => {
    onChange?.({ target: { value: v }, currentTarget: { value: v } } as unknown as ChangeEvent<HTMLSelectElement>);
    close();
  };

  return (
    <>
      <button
        ref={button}
        type="button"
        disabled={disabled}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={ariaLabel ? `${ariaLabel}: ${label}` : label}
        onClick={() => setOpen(true)}
        className={`${className ?? ''} flex items-center justify-between gap-2 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus disabled:opacity-50`}
      >
        <span className="truncate">{label}</span>
        <span aria-hidden className="text-text-muted">▾</span>
      </button>
      {open && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4" style={{ background: 'rgb(0 0 0 / 0.45)' }} onClick={close}>
          <div
            ref={list}
            role="menu"
            aria-label={ariaLabel}
            onClick={(e) => e.stopPropagation()}
            onKeyDown={(e) => {
              if (e.key === 'Backspace') {
                e.preventDefault();
                close();
                return;
              }
              // Flechas, Inicio y Fin recorren las opciones (teclado en el ordenador; en la TV, el mando hace lo mismo).
              const items = [...(list.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not([disabled])') ?? [])];
              const at = items.indexOf(document.activeElement as HTMLButtonElement);
              const go = { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: items.length - 1 }[e.key as 'ArrowDown'];
              if (go !== undefined && items.length) {
                e.preventDefault();
                items[Math.max(0, Math.min(items.length - 1, go))].focus();
              }
            }}
            className="flex max-h-[80vh] w-[min(32rem,90vw)] flex-col gap-1 overflow-y-auto rounded-xl bg-surface-raised p-2 shadow-pop ring-1 ring-white/10"
          >
            {ariaLabel && <p className="px-3 py-1 text-xs font-semibold text-text-muted">{ariaLabel}</p>}
            {opts.map((o) => (
              <button
                key={o.value}
                type="button"
                role="menuitem"
                data-value={o.value}
                disabled={o.disabled}
                aria-current={o.value === current ? 'true' : undefined}
                onClick={() => choose(o.value)}
                className={`rounded-md px-3 py-2 text-left text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus disabled:opacity-40 ${
                  o.value === current ? 'bg-surface-overlay font-semibold text-text' : 'text-text'
                } focus:bg-accent focus:text-on-accent`}
              >
                {o.label}
              </button>
            ))}
          </div>
        </div>
      )}
    </>
  );
}
