import {Household} from './screens/Household';
import { SwitchProfile } from './screens/SwitchProfile';
import { LinkDevice } from './screens/LinkDevice';
import { AccountSecurity, RecoverAccount } from './screens/AccountSecurity';
import { Collections } from './screens/Collections';
import { Person } from './screens/Person';
import { StrictMode, type ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { ErrorBoundary } from './components/errorBoundary';
import './styles/base.css';
import { isTv } from './lib/tv';
import { SessionProvider, useSession } from './lib/session';
import { Banner, Button, Loading } from './components/ui';
import { SetupWizard } from './screens/SetupWizard';
import { Login } from './screens/Login';
import { Home } from './screens/Home';
import { Backups } from './screens/Backups';
import { Movies } from './screens/Movies';
import { MovieDetail } from './screens/MovieDetail';
import { SeriesDetail } from './screens/SeriesDetail';
import { ItemRedirect } from './screens/ItemRedirect';
import { Player } from './screens/Player';
import { AdminDashboard } from './screens/AdminDashboard';
import { AdminMetadata } from './screens/AdminMetadata';
import { AdminLibraries } from './screens/AdminLibraries';
import { AdminUsers } from './screens/AdminUsers';
import { Search } from './screens/Search';
import { AppShell } from './components/shell';
import { PinFailuresNotice, ProfileLock } from './components/profileLock';
import { MyAccount } from './screens/MyAccount';

// Decide a qué pantalla se puede ir según el estado: asistente pendiente → S01; sin sesión → S02.
function Gate({ need, children }: { need: 'setup' | 'anon' | 'user' | 'admin'; children: ReactNode }) {
  const { info, user, loading, error, refresh } = useSession();
  const { pathname } = useLocation();
  // Mientras se comprueba la sesión no se redirige: recargar un enlace profundo (/ver/…) no debe llevar al inicio.
  if (loading && (!info || (!user && !info.setup_required))) return <Loading />;
  if (error && !info)
    return (
      <main className="mx-auto flex max-w-md flex-col gap-4 p-6">
        <Banner tone="error">{error}</Banner>
        <Button onClick={() => void refresh()}>Reintentar</Button>
      </main>
    );
  if (info?.setup_required) return need === 'setup' ? <>{children}</> : <Navigate to="/configurar" replace />;
  if (need === 'setup') return <Navigate to="/" replace />;
  if (!user) return need === 'anon' ? <>{children}</> : <Navigate to="/entrar" replace />;
  if (need === 'anon') return <Navigate to="/" replace />;
  // Perfil bloqueado por el servidor: nada privado hasta escribir el PIN (cambiar de perfil sí se puede).
  if (user.locked && pathname !== '/perfiles') return <ProfileLock />;
  if (need === 'admin' && (!user.is_admin || isTv)) return <Navigate to="/" replace />;
  return <>{children}</>;
}

function App() {
  const location = useLocation();
  return (
    <ErrorBoundary resetKey={location.pathname + location.search}>
    <Routes>
      <Route path="/configurar" element={<Gate need="setup"><SetupWizard /></Gate>} />
      <Route path="/vincular" element={<LinkDevice />} />
      <Route path="/recuperar" element={<RecoverAccount />} />
      <Route path="/hogar" element={<Gate need="user"><Household /></Gate>} />
      <Route path="/cuenta" element={<Gate need="user"><MyAccount /></Gate>} />
      <Route path="/cuenta/seguridad" element={<Gate need="user"><AccountSecurity /></Gate>} />
      <Route path="/perfiles" element={<Gate need="user"><SwitchProfile /></Gate>} />
      <Route path="/entrar" element={<Gate need="anon"><Login /></Gate>} />
      <Route path="/ver/:id" element={<Gate need="user"><Player /></Gate>} />
      {/* Todo lo demás va dentro de la estructura con barra lateral (E1.2). */}
      <Route element={<Gate need="user"><AppShell /></Gate>}>
        <Route path="/admin" element={<Gate need="admin"><AdminDashboard /></Gate>} />
        <Route path="/admin/metadatos" element={<Gate need="admin"><AdminMetadata /></Gate>} />
        <Route path="/admin/copias" element={<Gate need="admin"><Backups /></Gate>} />
        <Route path="/admin/bibliotecas" element={<Gate need="admin"><AdminLibraries /></Gate>} />
        <Route path="/admin/usuarios" element={<Gate need="admin"><AdminUsers /></Gate>} />
        <Route path="/biblioteca/:id" element={<Movies />} />
        <Route path="/buscar" element={<Search />} />
        <Route path="/colecciones" element={<Collections />} />
        <Route path="/coleccion/:id" element={<Collections />} />
        <Route path="/persona/:id" element={<Person />} />
        <Route path="/pelicula/:id" element={<MovieDetail />} />
        <Route path="/serie/:id" element={<SeriesDetail />} />
        <Route path="/ficha/:id" element={<ItemRedirect />} />
        <Route path="*" element={<Home />} />
      </Route>
    </Routes>
    <PinFailuresNotice />
    </ErrorBoundary>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <SessionProvider>
        <App />
      </SessionProvider>
    </BrowserRouter>
  </StrictMode>,
);
