// S13 (parte de E1.1) · Usuarios y permisos por biblioteca. Matriz usuario × biblioteca con vista previa de lo que verá
// cada uno. Un usuario nuevo no tiene acceso a nada; los administradores ven todas las bibliotecas.
import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { ShieldCheck, UserPlus } from 'lucide-react';
import { api, ApiError } from '../lib/api';
import { KIND_LABEL, type Library } from '../lib/catalog';
import { AdminTabs } from '../components/shell';
import { Banner, Button, Card, Loading, TextField } from '../components/ui';

interface UserRow {
  id: string;
  username: string;
  display_name: string;
  is_admin: boolean;
 household_id:string|null;
 household_owner:boolean;
 passwordless:boolean;
 has_pin:boolean;
  is_disabled: boolean;
  last_login_at: string | null;
  libraries: string[];
  /** Reproducciones simultáneas de todo su hogar; null: el valor general (Panel → Reproducción). */
  max_streams: number | null;
  /** Miembros que puede crear en su hogar, sin contarle a él. */
  max_household_members: number;
  household_members: number;
  /** Permiso para crear (y ampliar) su propio hogar. */
  can_create_household: boolean;
  /** Miembro en la papelera: se elimina definitivamente 30 días después. */
  deleted_at?: string;
  /** Intentos fallidos de PIN o código en 30 días (solo número y fecha). */
  pin_failures: number;
  pin_failure_last?: string;
}

const when = (s: string) => new Date(s).toLocaleString('es-ES', { dateStyle: 'medium', timeStyle: 'short' });
const isMember = (u: UserRow) => !!u.household_id && !u.household_owner;

function pinFailuresLine(u: UserRow) {
  if (!u.pin_failures || !u.pin_failure_last) return null;
  return (
    <span className="text-danger">
      {' · '}
      {u.pin_failures} {u.pin_failures === 1 ? 'intento fallido' : 'intentos fallidos'} de PIN en 30 días (último: {when(u.pin_failure_last)})
    </span>
  );
}

/** Los miembros de un hogar, bajo su propietario: estado y PIN, nunca su historial ni su actividad. */
function HouseholdMembers({ members }: { members: UserRow[] }) {
  const active = members.filter((m) => !m.deleted_at);
  const trashed = members.filter((m) => m.deleted_at);
  return (
    <div className="rounded-md border border-border p-3 text-sm">
      <p className="mb-2 font-semibold">
        Miembros del hogar: {active.length}
        {trashed.length > 0 && `, y ${trashed.length} en la papelera`}
      </p>
      {members.length === 0 ? (
        <p className="text-text-muted">Aún no ha añadido miembros.</p>
      ) : (
        <ul className="flex flex-col gap-1">
          {members.map((m) => (
            <li key={m.id} className="text-text-muted">
              <span className="text-text">{m.display_name}</span>
              {m.deleted_at
                ? ` · en la papelera desde el ${new Date(m.deleted_at).toLocaleDateString('es-ES')} (se elimina a los 30 días)`
                : m.is_disabled
                  ? ' · desactivado'
                  : ' · activo'}
              {m.has_pin ? ' · con PIN' : ' · sin PIN'}
              {pinFailuresLine(m)}
            </li>
          ))}
        </ul>
      )}
      <p className="mt-2 text-xs text-text-subtle">
        El historial, los avances y las listas de cada perfil son privados: ni el administrador ni el propietario pueden verlos.
      </p>
    </div>
  );
}

/** Límites de un usuario para todo su hogar: reproducciones simultáneas y miembros que puede crear. */
function UserLimits({ user, busy, onSave }: { user: UserRow; busy: boolean; onSave: (body: object, message: string) => void }) {
  const [own, setOwn] = useState(user.max_streams !== null);
  const [streams, setStreams] = useState(String(user.max_streams ?? 1));
  const [members, setMembers] = useState(String(user.max_household_members));
  useEffect(() => {
    setOwn(user.max_streams !== null);
    setStreams(String(user.max_streams ?? 1));
    setMembers(String(user.max_household_members));
  }, [user.max_streams, user.max_household_members]);
  const n = Number(streams), m = Number(members);
  const validStreams = !own || (Number.isInteger(n) && n >= 0 && n <= 1000);
  const household = user.household_owner || user.can_create_household;
  const validMembers = !household || (Number.isInteger(m) && m >= 0 && m <= 19);
  const changed = own !== (user.max_streams !== null) || (own && n !== user.max_streams) || (household && m !== user.max_household_members);
  const field = 'h-10 w-24 rounded-md border border-border-input bg-surface-raised px-2 text-sm text-text';
  return (
    <fieldset className="flex flex-col gap-3 rounded-md border border-border p-3">
      <legend className="px-1 text-sm font-semibold">Límites{user.household_owner ? ' de todo su hogar' : ''}</legend>
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <span className="min-w-56">Reproducciones simultáneas</span>
        <select value={own ? 'own' : 'general'} onChange={(e) => setOwn(e.target.value === 'own')} className={field + ' w-auto'} aria-label="Origen del límite de reproducciones">
          <option value="general">Las generales</option>
          <option value="own">Propias</option>
        </select>
        {own && (
          <input type="number" min={0} max={1000} value={streams} onChange={(e) => setStreams(e.target.value)} className={field} aria-label="Reproducciones simultáneas" />
        )}
        <span className="text-xs text-text-muted">{own ? '0 = sin límite.' : 'Las de Panel → Reproducción.'} Cuentan las de todos sus perfiles.</span>
      </div>
      {household && (
        <div className="flex flex-wrap items-center gap-3 text-sm">
          <span className="min-w-56">Miembros que puede crear</span>
          <input type="number" min={0} max={19} value={members} onChange={(e) => setMembers(e.target.value)} className={field} aria-label="Miembros del hogar" />
          <span className="text-xs text-text-muted">
            {user.household_owner ? `Tiene ${user.household_members}` : 'Aún no ha creado su hogar'}; sin contar al propietario (máximo 19: 20 perfiles). Bajarlo no elimina a nadie: solo impide añadir.
          </span>
        </div>
      )}
      <div>
        <Button
          size="sm"
          disabled={busy || !changed || !validStreams || !validMembers}
          onClick={() => {
            const body: Record<string, number | null> = { max_streams: own ? n : null };
            if (household) body.max_household_members = m;
            onSave(body, `Límites de ${user.display_name} guardados.`);
          }}
        >
          Guardar límites
        </Button>
      </div>
    </fieldset>
  );
}

export function AdminUsers() {
  const [house,setHouse]=useState({code:'',username:'',display_name:'',can_create_household:false});
  const [houseLibs,setHouseLibs]=useState<string[]>([]);
  const [checkedDevice,setCheckedDevice]=useState<{device_name:string;ip:string}|null>(null);
  const [users, setUsers] = useState<UserRow[] | null>(null);
  const [libs, setLibs] = useState<Library[]>([]);
  const [draft, setDraft] = useState<Record<string, Set<string>>>({});
  const [form, setForm] = useState({ username: '', display_name: '', password: '' });
  const [notice, setNotice] = useState<{ tone: 'info' | 'error'; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [reset,setReset]=useState<UserRow|null>(null);
  const [adminPassword,setAdminPassword]=useState(''),[newPassword,setNewPassword]=useState('');

  const load = useCallback(async () => {
    try {
      const [u, l] = await Promise.all([
        api<{ users: UserRow[] }>('GET', '/api/v1/admin/users'),
        api<{ libraries: Library[] }>('GET', '/api/v1/libraries'),
      ]);
      setUsers(u.users);
      setLibs(l.libraries);
      setDraft(Object.fromEntries(u.users.map((x) => [x.id, new Set(x.libraries)])));
    } catch (e) {
      setNotice({ tone: 'error', text: e instanceof ApiError ? e.message : 'No se han podido cargar los usuarios.' });
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  async function run<T>(action: () => Promise<T>, ok: string | ((r: T) => string)) {
    setBusy(true);
    setNotice(null);
    try {
      const r = await action();
      setNotice({ tone: 'info', text: typeof ok === 'string' ? ok : ok(r) });
      await load();
    } catch (e) {
      setNotice({ tone: 'error', text: e instanceof ApiError ? e.message : 'La operación ha fallado.' });
    } finally {
      setBusy(false);
    }
  }

  function toggle(userId: string, libId: string) {
    setDraft((d) => {
      const next = new Set(d[userId]);
      if (next.has(libId)) next.delete(libId);
      else next.add(libId);
      return { ...d, [userId]: next };
    });
  }

  function createUser(e: FormEvent) {
    e.preventDefault();
    void run(async () => {
      await api('POST', '/api/v1/admin/users', form);
      setForm({ username: '', display_name: '', password: '' });
    }, 'Usuario creado sin acceso a ninguna biblioteca. Márcale las que pueda ver.');
  }

  const changed = (u: UserRow) => {
    const d = draft[u.id];
    return !!d && (d.size !== u.libraries.length || u.libraries.some((id) => !d.has(id)));
  };

  return (
    <div className="flex min-h-full flex-col">
      <main className="mx-auto flex w-full max-w-content flex-col gap-6 p-4 desktop:p-6">
        <AdminTabs />
        <h1 className="text-xl">Usuarios y acceso a bibliotecas</h1>
        <p className="text-sm text-text-muted">
          Cada usuario solo ve, busca y reproduce las bibliotecas marcadas. El cambio se aplica en su siguiente acción y corta, en
          menos de 30 segundos, lo que esté reproduciendo de una biblioteca retirada.
        </p>
        {notice && <Banner tone={notice.tone}>{notice.text}</Banner>}
        <Card><h2 className="mb-3 text-lg">Dar de alta una cuenta sin contraseña</h2><p className="mb-4 text-sm text-text-muted">El usuario abre Velora en su dispositivo y te facilita el código. Compruébalo antes de crear su cuenta; su dispositivo quedará autorizado. Si le das permiso de hogar, podrá crear su propio hogar desde «Mi cuenta».</p><form className="flex flex-col gap-3" onSubmit={e=>{e.preventDefault();void run(async()=>{await api('POST','/api/v1/admin/accounts/passwordless',{...house,library_ids:houseLibs});setHouse({code:'',username:'',display_name:'',can_create_household:false});setHouseLibs([]);setCheckedDevice(null);},'Cuenta creada y dispositivo autorizado sin contraseña.');}}>
 <TextField label="Código de seis dígitos del dispositivo" inputMode="numeric" maxLength={6} required value={house.code} onChange={e=>{setHouse({...house,code:e.target.value.replace(/\D/g,'')});setCheckedDevice(null);}}/>
 <Button type="button" variant="secondary" disabled={busy||house.code.length!==6} onClick={()=>void run(async()=>{setCheckedDevice(null);setCheckedDevice(await api('POST','/api/v1/auth/device/inspect',{code:house.code}));},'Dispositivo comprobado. Confirma que corresponde al usuario.')}>Comprobar dispositivo</Button>
 {checkedDevice&&<p>{checkedDevice.device_name} · {checkedDevice.ip}</p>}
 <TextField label="Usuario" required value={house.username} onChange={e=>setHouse({...house,username:e.target.value})}/><TextField label="Nombre visible" required maxLength={60} value={house.display_name} onChange={e=>setHouse({...house,display_name:e.target.value})}/>
 <fieldset className="flex flex-wrap gap-4"><legend>Bibliotecas que podrá ver (también su hogar, si lo crea)</legend>{libs.filter(l=>!l.retired_at).map(l=><label key={l.id} className="flex items-center gap-2"><input type="checkbox" checked={houseLibs.includes(l.id)} onChange={e=>setHouseLibs(old=>e.target.checked?[...old,l.id]:old.filter(id=>id!==l.id))}/>{l.name}</label>)}</fieldset>
 <label className="flex items-center gap-2"><input type="checkbox" className="h-4 w-4 accent-[var(--color-accent)]" checked={house.can_create_household} onChange={e=>setHouse({...house,can_create_household:e.target.checked})}/>Puede crear su propio hogar</label>
 <Button disabled={busy||!checkedDevice||!house.username||!house.display_name}>Crear la cuenta y autorizar el dispositivo</Button></form></Card>
        {!users && <Loading />}
        {reset&&<Card><form className="flex flex-col gap-3" onSubmit={e=>{e.preventDefault();void run(async()=>{const r=await api<{sealed:boolean}>('POST',`/api/v1/admin/users/${reset.id}/password`,{password:newPassword,admin_password:adminPassword});setReset(null);setAdminPassword('');setNewPassword('');return r;},r=>r.sealed?'Contraseña restablecida y sesiones cerradas. Como tenía PIN, su historial queda guardado aparte 30 días: solo su código de recuperación anterior lo devuelve.':'Contraseña restablecida. Las sesiones anteriores se han cerrado.');}}><h2 className="text-lg">Restablecer contraseña de {reset.display_name}</h2>{reset.has_pin&&<Banner tone="info">Esta cuenta tiene PIN. Para que la contraseña nueva (que tú conoces) no dé acceso a su historial protegido, al restablecerla su historial se guardará aparte 30 días y su PIN se quitará. Solo su código de recuperación anterior lo devuelve.</Banner>}<TextField label="Tu contraseña de administrador" type="password" autoComplete="current-password" value={adminPassword} onChange={e=>setAdminPassword(e.target.value)}/><TextField label="Contraseña nueva del usuario" type="password" autoComplete="new-password" value={newPassword} onChange={e=>setNewPassword(e.target.value)}/><Button disabled={busy||newPassword.length<10||!adminPassword}>Restablecer y cerrar sesiones</Button><Button type="button" variant="ghost" onClick={()=>{setReset(null);setAdminPassword('');setNewPassword('');}}>Cancelar</Button></form></Card>}
        {users?.filter((u) => !isMember(u)).map((u) => {
          const d = draft[u.id] ?? new Set<string>();
          // Las retiradas se muestran para no perder su permiso al guardar, pero no cuentan en lo que verá.
          const preview = libs.filter((l) => d.has(l.id) && !l.retired_at).map((l) => l.name);
          return (
            <Card key={u.id}>
              <div className="flex flex-col gap-3">
                <div className="flex flex-wrap items-center gap-3">
                  <div className="min-w-0 flex-1">
                    <p className="font-semibold">
                      {u.display_name}{u.household_owner?' · Propietario de un hogar':''} <span className="text-sm font-normal text-text-muted">@{u.username}</span>
                    </p>
                    <p className="text-xs text-text-muted">
                      {u.is_disabled ? 'Desactivado' : u.last_login_at ? `Último acceso: ${new Date(u.last_login_at).toLocaleString('es-ES')}` : 'Aún no ha entrado'}
                      {u.passwordless ? ' · sin contraseña' : ''}
                      {u.has_pin ? ' · con PIN' : ''}
                      {pinFailuresLine(u)}
                    </p>
                  </div>
                  {!u.is_admin&&!u.passwordless&&<Button variant="ghost" size="sm" disabled={busy} onClick={()=>{setReset(u);setAdminPassword('');setNewPassword('');}}>Restablecer contraseña</Button>}
                  {!u.is_admin&&u.has_pin&&u.passwordless&&<Button variant="ghost" size="sm" disabled={busy} onClick={()=>{if(window.confirm(`¿Restablecer el PIN de ${u.display_name}? Hazlo solo si lo ha olvidado y no tiene su código de recuperación. Se cerrarán sus sesiones y su historial se guardará aparte 30 días: solo su código anterior lo devuelve; tú no podrás verlo.`))void run(()=>api('POST',`/api/v1/admin/users/${u.id}/reset-pin`,{}),`PIN de ${u.display_name} restablecido. Su historial queda guardado aparte 30 días.`);}}>Restablecer PIN</Button>}
                  {!u.is_admin && (
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={busy}
                      onClick={() =>
                        void run(
                          () => api('PATCH', `/api/v1/admin/users/${u.id}`, { is_disabled: !u.is_disabled }),
                          u.is_disabled ? 'Usuario reactivado.' : 'Usuario desactivado y sesiones cerradas.',
                        )
                      }
                    >
                      {u.is_disabled ? 'Reactivar' : 'Desactivar'}
                    </Button>
                  )}
                </div>
                {u.is_admin ? (
                  <p className="inline-flex items-center gap-2 text-sm text-text-muted">
                    <ShieldCheck aria-hidden className="h-4 w-4" /> Administrador: ve todas las bibliotecas.
                  </p>
                ) : (
                  <>
                    <label className="inline-flex items-center gap-2 text-sm">
                      <input
                        type="checkbox"
                        className="h-4 w-4 accent-[var(--color-accent)]"
                        checked={u.can_create_household}
                        disabled={busy}
                        onChange={(e) =>
                          void run(
                            () => api('PATCH', `/api/v1/admin/users/${u.id}`, { can_create_household: e.target.checked }),
                            e.target.checked
                              ? `${u.display_name} puede crear su hogar y añadir miembros.`
                              : `Permiso de hogar retirado a ${u.display_name}. Sus miembros siguen; no podrá añadir ni recuperar más.`,
                          )
                        }
                      />
                      Puede crear su propio hogar{u.household_owner ? ' (ya lo tiene; sin permiso no puede añadir ni recuperar miembros)' : ''}
                    </label>
                    {u.household_owner && <HouseholdMembers members={users.filter((m) => isMember(m) && m.household_id === u.household_id)} />}
                    <fieldset className="flex flex-wrap gap-x-6 gap-y-2">
                      <legend className="sr-only">Bibliotecas de {u.display_name}</legend>
                      {libs.map((l) => (
                        <label key={l.id} className="inline-flex items-center gap-2 text-sm">
                          <input type="checkbox" checked={d.has(l.id)} onChange={() => toggle(u.id, l.id)} className="h-4 w-4 accent-[var(--color-accent)]" />
                          {l.name}{' '}
                          <span className="text-text-subtle">
                            ({KIND_LABEL[l.kind]}
                            {l.retired_at ? ', retirada' : ''})
                          </span>
                        </label>
                      ))}
                      {libs.length === 0 && <span className="text-sm text-text-muted">Aún no hay bibliotecas.</span>}
                    </fieldset>
                    <p className="text-sm text-text-muted">
                      Verá: {preview.length ? preview.join(', ') : 'nada (sin acceso a ninguna biblioteca)'}
                    </p>
                    <div>
                      <Button
                        size="sm"
                        disabled={busy || !changed(u)}
                        onClick={() =>
                          void run(
                            () => api('PUT', `/api/v1/admin/users/${u.id}/libraries`, { library_ids: [...d] }),
                            `Acceso de ${u.display_name} guardado.`,
                          )
                        }
                      >
                        Guardar acceso
                      </Button>
                    </div>
                    <UserLimits user={u} busy={busy} onSave={(body, message) => void run(() => api('PATCH', `/api/v1/admin/users/${u.id}`, body), message)} />
                  </>
                )}
              </div>
            </Card>
          );
        })}

        <Card>
          <form onSubmit={createUser} className="flex flex-col gap-4">
            <h2 className="inline-flex items-center gap-2 text-lg">
              <UserPlus aria-hidden className="h-5 w-5" /> Cuenta tradicional con contraseña (opcional)
            </h2>
            <TextField label="Usuario" value={form.username} autoComplete="off" onChange={(e) => setForm({ ...form, username: e.target.value })} />
            <TextField label="Nombre visible" value={form.display_name} onChange={(e) => setForm({ ...form, display_name: e.target.value })} />
            <TextField
              label="Contraseña"
              type="password"
              autoComplete="new-password"
              value={form.password}
              hint="Al menos 10 caracteres."
              onChange={(e) => setForm({ ...form, password: e.target.value })}
            />
            <Button type="submit" disabled={busy || !form.username || !form.display_name || form.password.length < 10}>
              Crear usuario
            </Button>
          </form>
        </Card>
      </main>
    </div>
  );
}
