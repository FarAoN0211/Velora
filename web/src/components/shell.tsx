import { ProfileOverlay } from './profileOverlay';
// E1.2 · Estructura común (S03, imagen de referencia): barra lateral con las bibliotecas del usuario, buscador arriba y, en
// el móvil, menú lateral desplegable. El reproductor queda fuera de esta estructura (pantalla completa).
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Link, NavLink, Outlet, useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { ArrowUp, ArrowDown, ChevronDown, Film, Home, LayoutList, LogOut, Menu, Search, Settings, Smile, Tv, X } from 'lucide-react';
import { api } from '../lib/api';
import { invalidateMenu, useMenuLibraries, type MenuLibrary } from '../lib/catalog';
import { useSession } from '../lib/session';
import { isTv } from '../lib/tv';
import { useLiveCatalog } from '../lib/liveCatalog';
import { ErrorBoundary } from './errorBoundary';

export function Logo({ compact = false }: { compact?: boolean }) {
  return (
    <Link to="/" aria-label="VELORA, inicio" className="flex items-center gap-2 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus">
      <img src="/brand/velora-icon-64.png" alt="" className={compact ? 'h-8 w-8 object-contain' : 'h-9 w-9 object-contain tablet:hidden'} />
      {!compact && <img src="/brand/velora-logo.webp" alt="" className="hidden h-12 w-40 object-contain object-left tablet:block tv:w-36" />}
    </Link>
  );
}

function libraryIcon(l: MenuLibrary) {
  if (/anim/i.test(l.name)) return Smile;
  return l.kind === 'tv' ? Tv : Film;
}

const itemClass = ({ isActive }: { isActive: boolean }) =>
  `flex h-11 tv:h-9 shrink-0 items-center gap-3 rounded-md px-3 text-sm transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus ${
    isActive ? 'bg-surface-overlay text-text font-semibold' : 'text-text-muted hover:bg-surface-raised hover:text-text'
  }`;

/** «Personalizar menú»: cada usuario elige qué bibliotecas con acceso ve en su barra lateral. */
function MenuEditor({ onClose }: { onClose: () => void }) {
  const libraries = useMenuLibraries().filter((l) => !l.retired_at);
  const [busy, setBusy] = useState<string | null>(null);
  const [error,setError]=useState('');
  useEffect(() => {
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    document.addEventListener('keydown', esc);
    return () => document.removeEventListener('keydown', esc);
  }, [onClose]);
  async function toggle(l: MenuLibrary) {
    setBusy(l.id);
    try {
      await api('PUT', `/api/v1/me/libraries/${l.id}/menu`, { visible: !l.in_menu });
      invalidateMenu();
    } catch {setError('No se ha podido guardar el cambio.');} finally {
      setBusy(null);
    }
  }
  async function move(id:string,step:number){
    const order=libraries.map(l=>l.id),at=order.indexOf(id),to=at+step;
    if(to<0||to>=order.length)return;
    [order[at],order[to]]=[order[to],order[at]];setBusy(id);setError('');
    try{await api('PUT','/api/v1/me/libraries/order',{ids:order});invalidateMenu();window.dispatchEvent(new Event('velora:catalogo'));}catch{setError('No se ha podido guardar el orden.');}finally{setBusy(null);}
  }
  return (
    <ProfileOverlay label="Personalizar menú" onClose={onClose}>
      <div>
        <div className="mb-1 flex items-center justify-between">
          <h2 id="menu-editor" className="text-lg font-semibold">
            Personalizar menú
          </h2>
          <button type="button" aria-label="Cerrar" onClick={onClose} className="rounded-full p-1 text-text-muted hover:text-text">
            <X aria-hidden className="h-5 w-5" />
          </button>
        </div>
        <p className="mb-4 text-sm text-text-muted">Ordena las bibliotecas con las flechas. El mismo orden se aplica al menú lateral y a los carruseles de Inicio. Las ocultas siguen en Inicio y en la búsqueda.</p>
        {error&&<p role="alert" className="mb-2 text-sm">{error}</p>}
        <ul className="flex flex-col gap-1">
          {libraries.map((l,index) => {
            const Icon = libraryIcon(l);
            return (
              <li key={l.id} className="flex items-center gap-1">
                <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-3 rounded-md px-2 py-2 hover:bg-surface-raised">
                  <Icon aria-hidden className="h-5 w-5 text-text-muted" />
                  <span className="flex-1 text-sm">{l.name}</span>
                  <input
                    type="checkbox"
                    role="switch"
                    checked={l.in_menu}
                    disabled={busy !== null}
                    onChange={() => void toggle(l)}
                    className="h-5 w-5 accent-[var(--color-accent)]"
                  />
                </label>
                <button type="button" aria-label={`Subir ${l.name}`} disabled={busy!==null||index===0} onClick={()=>void move(l.id,-1)} className="rounded-md p-2 disabled:opacity-30"><ArrowUp aria-hidden className="h-5 w-5"/></button>
                <button type="button" aria-label={`Bajar ${l.name}`} disabled={busy!==null||index===libraries.length-1} onClick={()=>void move(l.id,1)} className="rounded-md p-2 disabled:opacity-30"><ArrowDown aria-hidden className="h-5 w-5"/></button>
              </li>
            );
          })}
          {libraries.length === 0 && <li className="text-sm text-text-muted">No tienes acceso a ninguna biblioteca.</li>}
        </ul>
      </div>
    </ProfileOverlay>
  );
}

function ProfileMenu({ placement }: { placement: 'sidebar' | 'bar' }) {
  const { user, logout } = useSession();
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    // Escape es también el botón Atrás del mando.
    const esc = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      setOpen(false);
      ref.current?.querySelector('button')?.focus();
    };
    document.addEventListener('keydown', esc);
    return () => {
      document.removeEventListener('keydown', esc);
    };
  }, [open]);
  if (!user) return null;
  const initial = (user.display_name || user.username).slice(0, 1).toUpperCase();
  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-3 rounded-md p-2 text-left text-sm hover:bg-surface-raised focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
      >
        <span aria-hidden className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-accent font-semibold text-on-accent">
          {initial}
        </span>
        {placement === 'sidebar' && (
          <>
            <span className="min-w-0 flex-1 truncate text-text">{user.display_name}</span>
            <ChevronDown aria-hidden className="h-4 w-4 text-text-muted" />
          </>
        )}
        <span className="sr-only">Menú de {user.display_name}</span>
      </button>
      {open && (
        <ProfileOverlay label="Menú de usuario" placement={placement==='sidebar'?'above':'below'} menu anchor={ref.current?.getBoundingClientRect()} onClose={()=>setOpen(false)}>
          <Link role="menuitem" to="/perfiles" onClick={()=>setOpen(false)} className="flex w-full items-center gap-2 rounded-sm px-3 py-2 text-sm hover:bg-surface-raised">Cambiar usuario</Link>
          {user.is_admin && !isTv && (
            <Link
              role="menuitem"
              to="/admin"
              onClick={() => setOpen(false)}
              className="flex w-full items-center gap-2 rounded-sm px-3 py-2 text-sm text-text hover:bg-surface-raised"
            >
              <Settings aria-hidden className="h-4 w-4" /> Panel de control
            </Link>
          )}
          <button
            role="menuitem"
            type="button"
            onClick={() => {
              setOpen(false);
              setEditing(true);
            }}
            className="flex w-full items-center gap-2 rounded-sm px-3 py-2 text-sm text-text hover:bg-surface-raised"
          >
            <LayoutList aria-hidden className="h-4 w-4" /> Personalizar menú
          </button>
          <Link role="menuitem" to="/cuenta" onClick={()=>setOpen(false)} className="flex w-full items-center gap-2 rounded-sm px-3 py-2 text-sm text-text hover:bg-surface-raised">{user.household_id&&!user.household_owner?'Mi perfil':'Mi cuenta'}</Link>
          {user.household_id&&<Link role="menuitem" to="/hogar" onClick={()=>setOpen(false)} className="flex w-full items-center gap-2 rounded-sm px-3 py-2 text-sm text-text hover:bg-surface-raised">Mi hogar</Link>}
          {(!user?.household_id||user.household_owner)&&<Link role="menuitem" to="/vincular" onClick={()=>setOpen(false)} className="flex w-full items-center gap-2 rounded-sm px-3 py-2 text-sm text-text hover:bg-surface-raised">Vincular dispositivo</Link>}
          {!user?.passwordless&&<Link role="menuitem" to="/cuenta/seguridad" onClick={()=>setOpen(false)} className="flex w-full items-center gap-2 rounded-sm px-3 py-2 text-sm text-text hover:bg-surface-raised">Contraseña y códigos de acceso</Link>}
          <button
            role="menuitem"
            type="button"
            onClick={() => void logout()}
            className="flex w-full items-center gap-2 rounded-sm px-3 py-2 text-sm text-text hover:bg-surface-raised"
          >
            <LogOut aria-hidden className="h-4 w-4" /> Cerrar sesión
          </button>
        </ProfileOverlay>
      )}
      {editing && <MenuEditor onClose={() => setEditing(false)} />}
    </div>
  );
}

function Sidebar() {
  const { pathname } = useLocation();
  const libraries = useMenuLibraries().filter((l) => l.in_menu && !l.retired_at);
  // En TV, cambiar de sección no apila pantallas: Atrás desde cualquiera de ellas vuelve al inicio.
  const replace = isTv && pathname !== '/';
  return (
    <aside aria-label="Navegación" className="sticky top-0 hidden h-screen w-60 shrink-0 flex-col gap-2 border-r border-border bg-bg px-3 py-5 desktop:flex tv:flex tv:w-44 tv:gap-1 tv:px-2 tv:py-3">
      <div data-tv-omitir className="px-3 pb-4 tv:pb-2">
        <Logo />
      </div>
      <nav aria-label="Principal" className="flex flex-1 flex-col gap-1 overflow-y-auto">
        <NavLink to="/" end replace={replace} className={itemClass}>
          <Home aria-hidden className="h-5 w-5" /> Inicio
        </NavLink>
        <NavLink to="/buscar" replace={replace} className={itemClass}>
          <Search aria-hidden className="h-5 w-5" /> Buscar
        </NavLink>
        <NavLink to="/colecciones" className={itemClass}><LayoutList aria-hidden className="h-5 w-5" /> Colecciones</NavLink>
        {libraries.map((l) => {
          const Icon = libraryIcon(l);
          return (
            <NavLink key={l.id} to={`/biblioteca/${l.id}`} replace={replace} className={itemClass}>
              <Icon aria-hidden className="h-5 w-5 shrink-0" />
              <span className="truncate">{l.name}</span>
              {l.status === 'unavailable' && <span className="ml-auto h-2 w-2 rounded-full bg-status-unavailable" title="Inaccesible" />}
            </NavLink>
          );
        })}

      </nav>
      <ProfileMenu placement="sidebar" />
    </aside>
  );
}

export function SearchBox({ autoFocus = false }: { autoFocus?: boolean }) {
  const navigate = useNavigate();
  const location = useLocation();
  const [params] = useSearchParams();
  const [q, setQ] = useState(location.pathname === '/buscar' ? params.get('q') ?? '' : '');
  useEffect(() => {
    if (location.pathname !== '/buscar') setQ('');
  }, [location.pathname]);
  // Búsqueda en vivo (S09): al escribir se va a /buscar y se actualiza la consulta.
  useEffect(() => {
    if (!q.trim() && location.pathname !== '/buscar') return;
    const t = setTimeout(() => {
      if (location.pathname === '/buscar' || q.trim()) navigate(`/buscar?q=${encodeURIComponent(q.trim())}`, { replace: location.pathname === '/buscar' });
    }, 250);
    return () => clearTimeout(t);
  }, [q]); // eslint-disable-line react-hooks/exhaustive-deps
  function submit(e: FormEvent) {
    e.preventDefault();
    navigate(`/buscar?q=${encodeURIComponent(q.trim())}`, { replace: location.pathname === '/buscar' });
  }
  return (
    <form role="search" onSubmit={submit} className="relative min-w-0 w-full max-w-xl">
      <label className="sr-only" htmlFor="buscar-global">
        Buscar películas y series
      </label>
      <Search aria-hidden className="pointer-events-none absolute left-4 top-1/2 h-4 w-4 -translate-y-1/2 text-text-subtle" />
      <input
        id="buscar-global"
        type="search"
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="Buscar películas y series…"
        autoComplete="off"
        autoFocus={autoFocus}
        className="h-11 w-full rounded-full border border-border bg-surface pl-11 pr-4 text-sm tv:h-9 text-text placeholder:text-text-subtle focus:outline-none focus:ring-2 focus:ring-focus"
      />
    </form>
  );
}

function TopBar() {
  return (
    <header className="sticky top-0 z-20 flex h-16 items-center gap-2 bg-bg px-4 desktop:gap-4 desktop:px-8">
      <MobileNavigation />
      <div className="shrink-0 desktop:hidden">
        <Logo compact />
      </div>
      <SearchBox />
      <div className="ml-auto shrink-0 desktop:hidden">
        <ProfileMenu placement="bar" />
      </div>
    </header>
  );
}

/** Menú modal: todas las bibliotecas, desplazamiento propio y foco contenido en el panel. */
function MobileNavigation() {
  const [open, setOpen] = useState(false);
  const dialog = useRef<HTMLDialogElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const location = useLocation();
  const libraries = useMenuLibraries().filter((l) => l.in_menu && !l.retired_at);
  useEffect(() => { setOpen(false); }, [location.pathname, location.search]);
  useEffect(() => {
    if (!open) return;
    const panel = dialog.current;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    panel?.showModal();
    panel?.querySelector('button')?.focus();
    const desktop = window.matchMedia('(min-width: 1024px)');
    const resize = () => { if (desktop.matches) setOpen(false); };
    desktop.addEventListener('change', resize);
    return () => {
      desktop.removeEventListener('change', resize);
      panel?.close();
      document.body.style.overflow = overflow;
      trigger.current?.focus({ preventScroll: true });
    };
  }, [open]);
  return <>
    <button ref={trigger} type="button" aria-label="Abrir menú de bibliotecas" aria-haspopup="dialog"
      aria-expanded={open} aria-controls="menu-bibliotecas" onClick={() => setOpen(true)}
      className="flex h-11 w-11 shrink-0 items-center justify-center rounded-md text-text hover:bg-surface-raised focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus desktop:hidden">
      <Menu aria-hidden className="h-6 w-6" />
    </button>
    {open && <dialog ref={dialog} id="menu-bibliotecas" role="dialog" aria-modal="true" aria-label="Menú de bibliotecas"
      onCancel={(e) => { e.preventDefault(); setOpen(false); }}
      onKeyDown={(e) => {
        if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); setOpen(false); }
        if (e.key === 'Tab') {
          const elements = Array.from(e.currentTarget.querySelectorAll<HTMLElement>('a[href], button:not([disabled])'));
          const first = elements[0], last = elements[elements.length - 1];
          if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last?.focus(); }
          else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first?.focus(); }
        }
      }}
      onClick={(e) => { if (e.target === e.currentTarget) setOpen(false); }}
      className="fixed inset-y-0 left-0 m-0 h-[100dvh] max-h-none w-80 max-w-[85vw] border-r border-border bg-bg p-0 text-text shadow-pop backdrop:bg-scrim">
      <div className="flex h-full min-h-0 flex-col" onClick={(e) => e.stopPropagation()}>
        <div className="flex shrink-0 items-center justify-between gap-3 border-b border-border p-4">
          <Logo />
          <button type="button" aria-label="Cerrar menú de bibliotecas" onClick={() => setOpen(false)}
            className="flex h-11 w-11 items-center justify-center rounded-md text-text-muted hover:bg-surface-raised focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus">
            <X aria-hidden className="h-6 w-6" />
          </button>
        </div>
        <nav aria-label="Bibliotecas" className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto overscroll-contain p-3"
          onClick={(e) => { if ((e.target as HTMLElement).closest('a')) setOpen(false); }}>
          <NavLink to="/" end className={itemClass}><Home aria-hidden className="h-5 w-5" /> Inicio</NavLink>
          <NavLink to="/buscar" className={itemClass}><Search aria-hidden className="h-5 w-5" /> Buscar</NavLink>
          <NavLink to="/colecciones" className={itemClass}><LayoutList aria-hidden className="h-5 w-5" /> Colecciones</NavLink>
        {libraries.map((l) => { const Icon = libraryIcon(l); return <NavLink key={l.id} to={`/biblioteca/${l.id}`} className={itemClass}>
            <Icon aria-hidden className="h-5 w-5 shrink-0" /><span className="truncate">{l.name}</span>
            {l.status === 'unavailable' && <span className="ml-auto h-2 w-2 shrink-0 rounded-full bg-status-unavailable" title="Inaccesible" />}
          </NavLink>; })}
        </nav>
      </div>
    </dialog>}
  </>;
}

export function AppShell() {
  useLiveCatalog();
  const {user}=useSession();
  const location = useLocation();
  return (
    <div key={user?.id} className="flex min-h-full">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        {!isTv && <TopBar />}
        <ErrorBoundary resetKey={location.pathname + location.search}>
          <Outlet />
        </ErrorBoundary>
      </div>
    </div>
  );
}

/** Secciones del panel de control (se abre desde el perfil; solo administradores fuera del TV box). */
export function AdminTabs() {
  const cls = ({ isActive }: { isActive: boolean }) =>
    `shrink-0 rounded-full px-4 py-2 text-sm ${isActive ? 'bg-surface-overlay font-semibold text-text' : 'text-text-muted hover:text-text'}`;
  return (
    <div className="flex flex-col gap-3">
      <p className="flex items-center gap-2 text-sm font-semibold uppercase tracking-wider text-text-subtle">
        <Settings aria-hidden className="h-4 w-4" /> Panel de control
      </p>
      <nav aria-label="Panel de control" className="-mx-1 flex gap-2 overflow-x-auto px-1 pb-1 [scrollbar-width:none]">
        <NavLink to="/admin" end className={cls}>
          Resumen
        </NavLink>
        <NavLink to="/admin/bibliotecas" className={cls}>
          Bibliotecas
        </NavLink>
        <NavLink to="/admin/metadatos" className={cls}>
          Metadatos
        </NavLink>
        <NavLink to="/admin/usuarios" className={cls}>
          Usuarios
        </NavLink>
        <NavLink to="/admin/copias" className={cls}>
          Copias de seguridad
        </NavLink>
      </nav>
    </div>
  );
}
