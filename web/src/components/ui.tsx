// Primitivas de E0 según replica/design/components.md. Solo clases semánticas (tokens), nunca colores sueltos.
import { forwardRef, useId, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode } from 'react';
import { AlertTriangle, Info, Loader2 } from 'lucide-react';

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger';
type Size = 'sm' | 'md' | 'lg';

const variants: Record<Variant, string> = {
  primary: 'bg-accent text-on-accent hover:bg-accent-strong',
  secondary: 'bg-surface-raised text-text border border-border hover:bg-surface-overlay',
  ghost: 'text-text hover:bg-surface-raised',
  danger: 'bg-danger text-on-danger',
};
const sizes: Record<Size, string> = { sm: 'h-8 px-3 text-sm', md: 'h-10 px-4 text-sm', lg: 'h-12 px-6 text-base' };

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'primary', size = 'md', loading = false, disabled, children, className = '', ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      className={`inline-flex min-h-touch items-center justify-center gap-2 rounded-md font-semibold transition-colors duration-fast
        focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus focus-visible:ring-offset-2 focus-visible:ring-offset-bg
        disabled:cursor-not-allowed disabled:opacity-50 ${variants[variant]} ${sizes[size]} ${className}`}
      {...rest}
    >
      {loading && <Loader2 aria-hidden className="h-4 w-4 animate-spin motion-reduce:animate-none" />}
      {children}
    </button>
  );
});

interface FieldProps extends InputHTMLAttributes<HTMLInputElement> {
  label: string;
  hint?: string;
  error?: string;
}

export const TextField = forwardRef<HTMLInputElement, FieldProps>(function TextField({ label, hint, error, className = '', ...rest }, ref) {
  const id = useId();
  const describedBy = [hint && `${id}-hint`, error && `${id}-err`].filter(Boolean).join(' ') || undefined;
  return (
    <div className={`flex flex-col gap-1 ${className}`}>
      <label htmlFor={id} className="text-sm font-semibold text-text">
        {label}
      </label>
      <input
        ref={ref}
        id={id}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        className={`h-10 rounded-md border bg-surface px-3 text-base text-text placeholder:text-text-subtle
          focus:outline-none focus:ring-2 focus:ring-accent ${error ? 'border-danger' : 'border-border-input'}`}
        {...rest}
      />
      {hint && !error && (
        <p id={`${id}-hint`} className="text-xs text-text-muted">
          {hint}
        </p>
      )}
      {error && (
        <p id={`${id}-err`} className="flex items-center gap-1 text-xs text-danger">
          <AlertTriangle aria-hidden className="h-3.5 w-3.5" />
          {error}
        </p>
      )}
    </div>
  );
});

export function Banner({ tone = 'info', children }: { tone?: 'info' | 'error'; children: ReactNode }) {
  const Icon = tone === 'error' ? AlertTriangle : Info;
  return (
    <div
      role={tone === 'error' ? 'alert' : 'status'}
      className={`flex items-start gap-2 rounded-md border px-4 py-3 text-sm ${
        tone === 'error' ? 'border-danger text-danger' : 'border-border text-text-muted'
      } bg-surface-raised`}
    >
      <Icon aria-hidden className="mt-0.5 h-4 w-4 shrink-0" />
      <div>{children}</div>
    </div>
  );
}

export function Card({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <div className={`rounded-lg bg-surface p-6 shadow-card ${className}`}>{children}</div>;
}

// Skeleton que respeta el tamaño final; a los 8 s avisa sin declarar error (design/README.md §4.1).
export function Loading({ slow }: { slow?: boolean }) {
  return (
    <div role="status" aria-live="polite" className="flex flex-col items-center gap-3 p-8 text-text-muted">
      <div className="h-10 w-48 animate-pulse rounded-md bg-surface-raised motion-reduce:animate-none" />
      <span className="sr-only">Cargando…</span>
      {slow && <p className="text-sm">Está tardando más de lo habitual.</p>}
    </div>
  );
}

export function initials(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0]?.toUpperCase() ?? '')
    .join('');
}
