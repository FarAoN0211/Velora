// «Mi hogar» (fase 5 de replica/PLAN_HOGARES_Y_PIN.md). El propietario gestiona nombre, miembros (dentro del límite que
// fija el administrador), la papelera de 30 días y los dispositivos. Nadie ve aquí el historial de nadie.
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { Link, Navigate } from 'react-router-dom';
import { api, ApiError, type User } from '../lib/api';
import { useSession } from '../lib/session';
import { Banner, Button, Card, Loading, TextField } from '../components/ui';
import { PinPad } from '../components/pinPad';

interface FamilyUser extends User {
  is_disabled: boolean;
}
interface Device {
  id: string;
  name: string;
  expires_at: string;
}
interface Trashed {
  id: string;
  display_name: string;
  deleted_at: string;
  purge_at: string;
}
interface HouseholdInfo {
  name: string;
  profiles: FamilyUser[];
  devices: Device[];
  can_manage: boolean;
  max_members: number;
  members: number;
  trash?: Trashed[];
  max_streams?: number;
  can_grow?: boolean;
  can_add?: boolean;
}
interface Confirm {
  title: string;
  text: ReactNode;
  label: string;
  danger?: boolean;
  /** Segunda confirmación (acciones que no se pueden deshacer). */
  again?: string;
  /** Pide el PIN del propietario antes de seguir. */
  ownerPin?: boolean;
  action: (ownerPin: string) => Promise<unknown>;
  done: string;
}

const day = (s: string) => new Date(s).toLocaleDateString('es-ES', { dateStyle: 'long' });

export function Household() {
  const { user, refresh } = useSession();
  const [data, setData] = useState<HouseholdInfo | null>(null);
  const [newName, setNewName] = useState('');
  const [houseName, setHouseName] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<{ id: string; name: string } | null>(null);
  const [confirm, setConfirm] = useState<Confirm | null>(null);
  const [stage, setStage] = useState<'ask' | 'again' | 'pin'>('ask');
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const confirmRef = useRef<HTMLDivElement>(null);

  const load = useCallback(async () => {
    if (!user?.household_id) return;
    setData(await api<HouseholdInfo>('GET', '/api/v1/me/household'));
  }, [user?.household_id]);
  useEffect(() => {
    void load().catch((e) => setError(e instanceof Error ? e.message : 'No se ha podido cargar el hogar.'));
  }, [load]);
  useEffect(() => {
    if (confirm) confirmRef.current?.querySelector<HTMLButtonElement>('button')?.focus();
  }, [confirm, stage]);

  if (!user) return null;
  // Sin hogar, «Crear mi hogar» está en «Mi cuenta».
  if (!user.household_id) return <Navigate to="/cuenta" replace />;
  if (!data) return error ? <main className="p-6"><Banner tone="error">{error}</Banner></main> : <Loading />;

  async function run(action: () => Promise<unknown>, message: string): Promise<boolean> {
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await action();
      await refresh();
      await load();
      setNotice(message);
      return true;
    } catch (e) {
      setError(e instanceof ApiError && e.code === 'credencial_incorrecta' ? 'Tu PIN no es correcto.' : e instanceof Error ? e.message : 'No se ha podido guardar.');
      return false;
    } finally {
      setBusy(false);
    }
  }
  function ask(c: Confirm) {
    setError('');
    setNotice('');
    setStage('ask');
    setConfirm(c);
  }
  async function confirmed(pin = '') {
    if (!confirm) return;
    if (stage === 'ask' && confirm.again) return setStage('again');
    if (stage !== 'pin' && confirm.ownerPin && user?.has_pin) return setStage('pin');
    const c = confirm;
    const ok = await run(() => c.action(pin), c.done);
    if (ok || stage !== 'pin') setConfirm(null);
    return ok;
  }

  const owner = data.can_manage;
  const full = data.members >= data.max_members;
  const addBlocked = !data.can_grow
    ? 'El administrador del servidor no permite ahora añadir ni recuperar miembros. Los que ya tienes siguen funcionando.'
    : full
      ? `Has llegado al máximo de perfiles (${data.max_members + 1}, contándote a ti). Elimina alguno o pide al administrador que lo amplíe.`
      : '';

  return (
    <main className="mx-auto flex w-full max-w-3xl flex-col gap-5 p-6">
      <h1 className="text-xl">{data.name || 'Mi hogar'}</h1>
      {error && !confirm && <Banner tone="error">{error}</Banner>}
      {notice && <Banner tone="info">{notice}</Banner>}

      {confirm && (
        <Card className="border-2 border-accent">
          <div ref={confirmRef} className="flex flex-col gap-3">
            <h2 className="text-lg">{confirm.title}</h2>
            {stage === 'pin' ? (
              <PinPad
                title="Confirma con tu PIN"
                busy={busy}
                error={error}
                allowLegacy
                onComplete={async (pin) => (await confirmed(pin)) !== false}
              >
                <Button type="button" variant="ghost" onClick={() => setConfirm(null)}>
                  Cancelar
                </Button>
              </PinPad>
            ) : (
              <>
                <div className="text-sm text-text-muted">{stage === 'again' ? confirm.again : confirm.text}</div>
                {error && <Banner tone="error">{error}</Banner>}
                <div className="flex flex-wrap gap-3">
                  <Button variant={confirm.danger ? 'danger' : 'primary'} disabled={busy} onClick={() => void confirmed()}>
                    {stage === 'again' ? 'Sí, eliminar para siempre' : confirm.label}
                  </Button>
                  <Button variant="ghost" onClick={() => setConfirm(null)}>
                    Cancelar
                  </Button>
                </div>
              </>
            )}
          </div>
        </Card>
      )}

      {owner && (
        <Card>
          <h2 className="mb-3 text-lg">Tu hogar</h2>
          {houseName === null ? (
            <div className="flex flex-wrap items-center gap-3">
              <span className="flex-1">Nombre: {data.name}</span>
              <Button variant="secondary" size="sm" onClick={() => setHouseName(data.name)}>
                Cambiar nombre
              </Button>
            </div>
          ) : (
            <form
              className="flex flex-wrap items-end gap-3"
              onSubmit={(e) => {
                e.preventDefault();
                void run(() => api('PATCH', '/api/v1/me/household', { name: houseName }), 'Nombre del hogar cambiado.').then((ok) => ok && setHouseName(null));
              }}
            >
              <TextField className="flex-1" label="Nombre del hogar" maxLength={60} autoFocus value={houseName} onChange={(e) => setHouseName(e.target.value)} />
              <Button disabled={busy || !houseName.trim()}>Guardar</Button>
              <Button type="button" variant="ghost" onClick={() => setHouseName(null)}>
                Cancelar
              </Button>
            </form>
          )}
          <ul className="mt-4 flex flex-col gap-1 text-sm text-text-muted">
            <li>
              Perfiles: {data.members + 1} de {data.max_members + 1}, contándote a ti.
            </li>
            <li>
              Reproducciones a la vez en todo el hogar: {data.max_streams ? data.max_streams : 'sin límite'}.
            </li>
            <li>Estos límites los fija el administrador del servidor.</li>
          </ul>
        </Card>
      )}

      <Card>
        <h2 className="mb-2 text-lg">Miembros</h2>
        <p className="mb-4 text-sm text-text-muted">
          Cada perfil tiene su propio historial, sus avances y, si quiere, su PIN. Nadie, ni el propietario ni el administrador,
          puede ver el historial de otro perfil.
        </p>
        <ul className="flex flex-col gap-4">
          {data.profiles.map((p) => (
            <li key={p.id} className="flex flex-col gap-2 border-b border-border pb-4 last:border-0 last:pb-0">
              {renaming?.id === p.id ? (
                <form
                  className="flex flex-wrap items-end gap-3"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void run(() => api('PATCH', `/api/v1/me/household/profiles/${p.id}`, { display_name: renaming.name }), 'Perfil renombrado.').then((ok) => ok && setRenaming(null));
                  }}
                >
                  <TextField className="flex-1" label={`Nuevo nombre de ${p.display_name}`} maxLength={60} autoFocus value={renaming.name} onChange={(e) => setRenaming({ id: p.id, name: e.target.value })} />
                  <Button disabled={busy || !renaming.name.trim()}>Guardar</Button>
                  <Button type="button" variant="ghost" onClick={() => setRenaming(null)}>
                    Cancelar
                  </Button>
                </form>
              ) : (
                <p>
                  <strong>{p.display_name}</strong>
                  <span className="text-sm text-text-muted">
                    {' · '}
                    {p.household_owner ? 'Propietario' : p.is_disabled ? 'Desactivado' : 'Miembro'}
                    {p.has_pin ? ' · Con PIN' : ' · Sin PIN'}
                    {p.id === user.id ? ' · Tú' : ''}
                  </span>
                </p>
              )}
              {owner && !p.household_owner && renaming?.id !== p.id && (
                <div className="flex flex-wrap gap-2">
                  <Button variant="secondary" size="sm" disabled={busy} onClick={() => setRenaming({ id: p.id, name: p.display_name })}>
                    Renombrar
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={busy}
                    onClick={() => void run(() => api('PATCH', `/api/v1/me/household/profiles/${p.id}`, { is_disabled: !p.is_disabled }), p.is_disabled ? `${p.display_name} reactivado.` : `${p.display_name} desactivado: no podrá entrar hasta que lo reactives.`)}
                  >
                    {p.is_disabled ? 'Reactivar' : 'Desactivar'}
                  </Button>
                  {p.has_pin && (
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={busy}
                      onClick={() =>
                        ask({
                          title: `Restablecer el PIN de ${p.display_name}`,
                          text: (
                            <>
                              <p className="mb-2">
                                Solo si {p.display_name} ha olvidado su PIN <strong>y no tiene su código de recuperación</strong> (con el
                                código puede hacerlo él mismo en «He olvidado mi PIN», sin perder nada).
                              </p>
                              <p>
                                Su perfil quedará sin PIN y vacío. Su historial y sus avances se guardarán aparte, donde nadie puede
                                verlos (tampoco tú), durante 30 días: solo vuelven con su código de recuperación anterior.
                              </p>
                            </>
                          ),
                          label: 'Restablecer su PIN',
                          danger: true,
                          ownerPin: true,
                          action: (pin) => api('PATCH', `/api/v1/me/household/profiles/${p.id}`, { reset_pin: true, owner_pin: pin }),
                          done: `PIN de ${p.display_name} restablecido. Puede elegir uno nuevo al abrir su perfil.`,
                        })
                      }
                    >
                      Restablecer PIN
                    </Button>
                  )}
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={busy}
                    onClick={() =>
                      ask({
                        title: `Eliminar a ${p.display_name}`,
                        text: `Su perfil desaparece al momento y se cierran sus sesiones. Durante 30 días estará en «Eliminados recientemente», con su historial intacto, por si quieres recuperarlo; después se borra para siempre.`,
                        label: 'Eliminar',
                        danger: true,
                        action: () => api('DELETE', `/api/v1/me/household/profiles/${p.id}`),
                        done: `${p.display_name} está en «Eliminados recientemente» durante 30 días.`,
                      })
                    }
                  >
                    Eliminar
                  </Button>
                </div>
              )}
            </li>
          ))}
        </ul>
        {owner && (
          <form
            className="mt-5 flex flex-col gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              void run(() => api('POST', '/api/v1/me/household/profiles', { display_name: newName }), `Perfil «${newName}» creado. Ya aparece en «Cambiar usuario».`).then((ok) => ok && setNewName(''));
            }}
          >
            <h3 className="font-semibold">Añadir un miembro</h3>
            {addBlocked ? (
              <p className="text-sm text-text-muted">{addBlocked}</p>
            ) : (
              <>
                <TextField label="Nombre del nuevo perfil" maxLength={60} required value={newName} onChange={(e) => setNewName(e.target.value)} />
                <div>
                  <Button disabled={busy || !newName.trim()}>Añadir miembro</Button>
                </div>
              </>
            )}
          </form>
        )}
      </Card>

      {owner && data.trash && data.trash.length > 0 && (
        <Card>
          <h2 className="mb-2 text-lg">Eliminados recientemente</h2>
          <p className="mb-4 text-sm text-text-muted">
            Se conservan 30 días con su historial. Para recuperar uno necesitas un hueco libre dentro de tu límite de perfiles.
          </p>
          <ul className="flex flex-col gap-3">
            {data.trash.map((t) => (
              <li key={t.id} className="flex flex-wrap items-center gap-3">
                <span className="flex-1">
                  {t.display_name} <span className="text-sm text-text-muted">· eliminado el {day(t.deleted_at)} · se borra el {day(t.purge_at)}</span>
                </span>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={busy || !!addBlocked}
                  title={addBlocked || undefined}
                  onClick={() => void run(() => api('POST', `/api/v1/me/household/profiles/${t.id}/restore`), `${t.display_name} recuperado con todo su historial.`)}
                >
                  Recuperar
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busy}
                  onClick={() =>
                    ask({
                      title: `Eliminar ya a ${t.display_name}`,
                      text: 'Se borrará ahora mismo, con su historial y sus avances. No se podrá recuperar.',
                      again: `¿Seguro? ${t.display_name} y todo su historial se borrarán para siempre.`,
                      label: 'Eliminar ya',
                      danger: true,
                      action: () => api('DELETE', `/api/v1/me/household/profiles/${t.id}/permanent`),
                      done: `${t.display_name} se ha eliminado definitivamente.`,
                    })
                  }
                >
                  Eliminar ya
                </Button>
              </li>
            ))}
          </ul>
          {addBlocked && <p className="mt-3 text-xs text-text-muted">{addBlocked}</p>}
        </Card>
      )}

      {owner && (
        <Card>
          <h2 className="mb-3 text-lg">Dispositivos del hogar</h2>
          <p className="mb-3 text-sm text-text-muted">En estos dispositivos se puede elegir cualquier perfil del hogar (los que tienen PIN lo piden).</p>
          <Link to="/vincular" className="text-accent underline">
            Vincular un dispositivo mediante QR o código
          </Link>
          <ul className="mt-4 flex flex-col gap-3">
            {data.devices.map((d) => (
              <li key={d.id} className="flex items-center justify-between gap-3">
                <span>{d.name}</span>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busy}
                  onClick={() =>
                    ask({
                      title: `Retirar «${d.name}»`,
                      text: 'Se cerrarán todas las sesiones del hogar en ese dispositivo. Para volver a usarlo habrá que vincularlo de nuevo.',
                      label: 'Retirar',
                      action: () => api('DELETE', `/api/v1/me/household/devices/${d.id}`),
                      done: 'Dispositivo retirado.',
                    })
                  }
                >
                  Retirar
                </Button>
              </li>
            ))}
            {data.devices.length === 0 && <li className="text-sm text-text-muted">Aún no hay dispositivos vinculados.</li>}
          </ul>
        </Card>
      )}

      {!owner && (
        <p className="text-sm text-text-muted">
          El propietario del hogar gestiona los miembros y los dispositivos. Tu PIN y cuándo se vuelve a pedir se eligen en «Mi perfil».
        </p>
      )}
      <div className="flex flex-wrap gap-4 text-sm">
        <Link to="/cuenta" className="text-accent">
          {user.household_owner ? 'Mi cuenta' : 'Mi perfil'}
        </Link>
        <Link to="/" className="text-accent">
          Volver a Velora
        </Link>
      </div>
    </main>
  );
}
