import { useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { api, ApiError, type User } from '../lib/api';
import { useSession } from '../lib/session';
import { Banner, Button, Card, Loading, TextField } from '../components/ui';
import { Login } from './Login';
import { isTv } from '../lib/tv';
interface Device {device_name:string;ip:string;expires_at:string}
export function LinkDevice(){
 const {info,user,loading}=useSession();const [params]=useSearchParams();
 const [targets,setTargets]=useState<User[]>([]),[target,setTarget]=useState(''),[pin,setPin]=useState('');
 const [code,setCode]=useState(params.get('codigo')??''),[device,setDevice]=useState<Device|null>(null),[error,setError]=useState(''),[done,setDone]=useState(false),[busy,setBusy]=useState(false);
 const inspect=async(value:string)=>{setBusy(true);setError('');setDevice(null);try{setDevice(await api<Device>('POST','/api/v1/auth/device/inspect',{code:value}));}catch(e){setError(e instanceof ApiError?e.message:'No se ha podido comprobar el código.');}finally{setBusy(false);}};
 useEffect(()=>{const q=params.get('codigo');if(q&&user&&/^\d{6}$/.test(q)){setCode(q);void inspect(q);}},[params,user?.id]);
 useEffect(()=>{if(!user)return;setTarget(user.id);if(user.is_admin)void api<{users:(User&{deleted_at?:string;is_disabled?:boolean})[]}>('GET','/api/v1/admin/users').then(r=>setTargets(r.users.filter(u=>!u.deleted_at&&!u.is_disabled)));else if(user.household_owner)void api<{profiles:User[]}>('GET','/api/v1/me/household').then(r=>setTargets(r.profiles));else setTargets([user]);},[user?.id]);
 if(loading&&!info)return <Loading/>;
 if(info?.setup_required)return <main className="mx-auto max-w-lg p-6"><Link to={`/configurar?codigo=${encodeURIComponent(code)}`}>Crear la primera cuenta de administrador</Link></main>;
 if(!user)return <Login returnTo={`/vincular${/^\d{6}$/.test(code)?`?codigo=${code}`:''}`}/>;
 if(user.household_id&&!user.household_owner)return <main className="p-6"><Banner tone="info">Pide al propietario de tu hogar que autorice este dispositivo para tu perfil.</Banner><Link to="/">Volver</Link></main>;
 const selected=targets.find(p=>p.id===target);
 const appLink=`velora://vincular?servidor=${encodeURIComponent(window.location.origin)}${/^\d{6}$/.test(code)?`&codigo=${code}`:''}`;
 return <main className="mx-auto flex max-w-lg flex-col gap-5 p-6"><h1 className="text-xl">Vincular dispositivo</h1>
 <p>Selecciona el perfil que se abrirá en el dispositivo. Si tiene PIN, debes introducirlo para autorizarlo.</p>
 <label className="flex flex-col gap-2">Perfil<select className="rounded-md border border-border bg-surface p-3" value={target} onChange={e=>{setTarget(e.target.value);setPin('');}}>{targets.map(p=><option key={p.id} value={p.id}>{p.display_name}</option>)}</select></label>
 {selected?.has_pin&&<TextField label={`PIN de ${selected.display_name}`} type="password" inputMode="numeric" maxLength={8} value={pin} onChange={e=>setPin(e.target.value.replace(/\D/g,''))}/>}
 {user.is_admin&&<Link className="text-accent underline" to="/admin/usuarios">Dar de alta una cuenta nueva con este código</Link>}
 {done?<Banner tone="info">Dispositivo autorizado. La sesión se abrirá en el televisor. Puedes cerrar esta página.</Banner>:<Card><form className="flex flex-col gap-4" onSubmit={e=>{e.preventDefault();void inspect(code);}}>
 <TextField label="Código de seis dígitos del televisor" inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={e=>{setCode(e.target.value.replace(/\D/g,''));setDevice(null);}}/>
 <Button disabled={busy||!/^\d{6}$/.test(code)}>Comprobar dispositivo</Button>
 {device&&<div className="flex flex-col gap-3"><p><strong>{device.device_name}</strong><br/>Dirección: {device.ip}</p><p className="text-sm text-text-muted">Confirma que el código coincide con el que estás viendo en tu televisor.</p><Button type="button" disabled={busy} onClick={async()=>{setBusy(true);setError('');try{await api('POST','/api/v1/auth/device/approve',{code,user_id:target,pin});setDone(true);}catch(e){setError(e instanceof Error?e.message:'No se ha podido autorizar.');}finally{setBusy(false);}}}>Autorizar este dispositivo</Button></div>}
 </form></Card>}
 {error&&<Banner tone="error">{error}</Banner>}
 {!isTv&&window.VeloraApp?.escanearVinculacion&&<Button variant="secondary" onClick={()=>window.VeloraApp?.escanearVinculacion?.()}>Escanear QR con Velora</Button>}
 {!window.VeloraApp&&<a href={appLink} className="text-accent underline">Abrir en la aplicación Velora</a>}
 <Link to="/" className="text-accent">Volver a Velora</Link>
 </main>;
}
