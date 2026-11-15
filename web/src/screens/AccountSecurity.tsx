import { useState } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../lib/api';
import { useSession } from '../lib/session';
import { Banner, Button, Card, TextField } from '../components/ui';
export function AccountSecurity(){
 const {user}=useSession();const [password,setPassword]=useState(''),[codes,setCodes]=useState<string[]>([]),[error,setError]=useState(''),[busy,setBusy]=useState(false);
 return <main className="mx-auto flex max-w-xl flex-col gap-5 p-6"><h1 className="text-xl">Acceso y recuperación</h1><p>Cuenta: <strong>{user?.username}</strong></p><Link to="/vincular" className="text-accent">Vincular un TV box</Link>
 <Card><form className="flex flex-col gap-4" onSubmit={async e=>{e.preventDefault();setBusy(true);setError('');setCodes([]);try{const r=await api<{codes:string[]}>('POST','/api/v1/me/recovery-codes',{password});setCodes(r.codes);setPassword('');}catch(e){setError(e instanceof Error?e.message:'No se han podido generar los códigos.');}finally{setBusy(false);}}}>
 <h2 className="text-lg">Códigos de recuperación</h2><p className="text-sm text-text-muted">Guárdalos fuera de Velora. Cada código sirve una sola vez, caduca en un año y permite restablecer tu contraseña. Generar una lista nueva invalida la anterior. Al recuperar la cuenta se invalidan todos los códigos y se cierran sus sesiones.</p>
 <TextField label="Confirma tu contraseña" type="password" autoComplete="current-password" value={password} onChange={e=>setPassword(e.target.value)}/><Button disabled={busy||!password}>Generar códigos</Button>
 {codes.length>0&&<><p>Solo se muestran ahora. Guarda esta lista en un lugar seguro.</p><pre className="select-text overflow-auto rounded bg-surface p-3 text-xs">{codes.join('\n')}</pre><Button type="button" variant="secondary" onClick={()=>{const url=URL.createObjectURL(new Blob([`Velora · ${user?.username}\n\n${codes.join('\n')}\n`],{type:'text/plain'}));const a=document.createElement('a');a.href=url;a.download='velora-recuperacion.txt';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}}>Guardar lista</Button></>}
 </form></Card>{error&&<Banner tone="error">{error}</Banner>}<Link to="/" className="text-accent">Volver</Link></main>;
}
export function RecoverAccount(){
 const [username,setUsername]=useState(''),[code,setCode]=useState(''),[password,setPassword]=useState(''),[repeat,setRepeat]=useState(''),[error,setError]=useState(''),[done,setDone]=useState(false),[busy,setBusy]=useState(false);
 return <main className="mx-auto flex min-h-full max-w-lg flex-col justify-center gap-5 p-6"><h1 className="text-xl">Recuperar acceso</h1>
 {done?<Banner tone="info">Contraseña cambiada. Las sesiones anteriores y los códigos de recuperación se han invalidado. Inicia sesión y genera una lista nueva.</Banner>:<Card><form className="flex flex-col gap-4" onSubmit={async e=>{e.preventDefault();if(password!==repeat){setError('Las contraseñas no coinciden.');return;}setBusy(true);setError('');try{await api('POST','/api/v1/auth/recover',{username,code,password});setDone(true);setPassword('');setRepeat('');setCode('');}catch(e){setError(e instanceof Error?e.message:'No se ha podido recuperar la cuenta.');}finally{setBusy(false);}}}>
 <TextField label="Usuario" value={username} autoComplete="username" onChange={e=>setUsername(e.target.value)}/><TextField label="Código personal de recuperación" value={code} autoComplete="off" onChange={e=>setCode(e.target.value)} hint="Usa uno de los códigos guardados; el código de seis dígitos del TV no sirve aquí."/>
 <TextField label="Contraseña nueva" type="password" autoComplete="new-password" value={password} onChange={e=>setPassword(e.target.value)} hint="Al menos 10 caracteres."/><TextField label="Repite la contraseña" type="password" autoComplete="new-password" value={repeat} onChange={e=>setRepeat(e.target.value)}/><Button disabled={busy||!username||!code||password.length<10}>Restablecer contraseña</Button>
 </form></Card>}
 {error&&<Banner tone="error">{error}</Banner>}<p className="text-sm text-text-muted">Si no tienes códigos, pide al administrador que restablezca tu contraseña. Para recuperar al administrador, el propietario puede generar un código de emergencia desde el servidor. No se necesita correo.</p><Link to="/entrar" className="text-accent">Volver al inicio de sesión</Link></main>;
}
