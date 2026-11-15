import {useEffect,useState} from 'react';
import {Link,useNavigate} from 'react-router-dom';
import {api,ApiError,type PinFailures,type Profile,type User} from '../lib/api';
import {useSession} from '../lib/session';
import {Banner,Button,initials} from '../components/ui';
import {PinPad,RecoverPin,RecoveryCodeNotice} from '../components/pinPad';
import {Login} from './Login';

// Cambiar usuario (fase 5 de replica/PLAN_HOGARES_Y_PIN.md): los perfiles con PIN lo piden con el teclado de cuatro
// casillas (también con el mando de la TV); «He olvidado mi PIN» usa el código de recuperación.
export function SwitchProfile(){
 const {user,setUser,setPinNotice}=useSession();const navigate=useNavigate();
 const [pinTarget,setPinTarget]=useState<Profile|null>(null),[recovering,setRecovering]=useState(false),[newCode,setNewCode]=useState<{code:string;user:User}|null>(null);
 const [profiles,setProfiles]=useState<Profile[]>([]),[adding,setAdding]=useState(false),[error,setError]=useState(''),[pinError,setPinError]=useState(''),[busy,setBusy]=useState(false);
 useEffect(()=>{const c=new AbortController();void api<{profiles:Profile[]}>('GET','/api/v1/me/profiles',undefined,c.signal).then(r=>setProfiles(r.profiles)).catch(()=>{if(!c.signal.aborted)setError('No se han podido cargar las cuentas recordadas.');});return()=>c.abort();},[]);
 async function enter(r:{user:User;pin_failures?:PinFailures}){setPinNotice(r.pin_failures??null);
  // Si alguien le restableció el PIN, al abrir el perfil se va a «Mi perfil»: elegir PIN nuevo y recuperar su historial.
  let to='/';if(!r.user.has_pin){const s=await api<{seals:unknown[]}>('GET','/api/v1/me/history/seals').catch(()=>null);if(s&&s.seals.length)to='/cuenta';}
  setUser(r.user);navigate(to,{replace:true});}
 async function choose(id:string,pinValue=''):Promise<boolean>{setBusy(true);setError('');setPinError('');try{await enter(await api<{user:User;pin_failures?:PinFailures}>('POST','/api/v1/auth/switch',{user_id:id,pin:pinValue}));return true;}catch(e){const msg=e instanceof ApiError&&e.code==='credencial_incorrecta'?'PIN incorrecto.':e instanceof Error?e.message:'No se ha podido cambiar de usuario.';if(pinTarget)setPinError(msg);else setError(msg);return false;}finally{setBusy(false);}}
 const close=()=>{setPinTarget(null);setRecovering(false);setPinError('');};
 if(adding)return <><Button variant="ghost" className="m-4" onClick={()=>setAdding(false)}>Volver a las cuentas recordadas</Button><Login/></>;
 if(newCode)return <main className="flex min-h-screen items-center justify-center p-6"><RecoveryCodeNotice code={newCode.code} name={newCode.user.display_name} onDone={()=>{setUser(newCode.user);navigate('/',{replace:true});}}/></main>;
 if(pinTarget)return <main className="flex min-h-screen flex-col items-center justify-center gap-6 p-6">
  {recovering?<RecoverPin profile={pinTarget} onCancel={()=>setRecovering(false)} onRecovered={r=>{setPinNotice(r.pin_failures??null);setNewCode({code:r.recovery_code,user:r.user});}}/>:
  <PinPad title={`PIN de ${pinTarget.display_name}`} busy={busy} error={pinError} allowLegacy onComplete={pin=>choose(pinTarget.id,pin)}>
   <Button type="button" variant="ghost" onClick={()=>{setPinError('');setRecovering(true);}}>He olvidado mi PIN</Button>
   <Button type="button" variant="ghost" size="sm" onClick={close}>Elegir otro perfil</Button>
  </PinPad>}
 </main>;
 return <main className="mx-auto flex min-h-screen max-w-3xl flex-col justify-center gap-6 p-6 text-center">
  <h1 className="text-xl tv:text-3xl">¿Quién va a ver Velora?</h1><p className="text-sm text-text-muted">Selecciona tu perfil. Los perfiles protegidos piden su PIN.</p>
  {error&&<Banner tone="error">{error}</Banner>}
  <ul className="flex flex-wrap justify-center gap-4">{profiles.map((p,i)=><li key={p.id}><button autoFocus={i===0} disabled={busy} onClick={()=>{setError('');if(p.id===user?.id&&!user.locked)navigate('/');else if(p.has_pin)setPinTarget(p);else void choose(p.id);}} className="flex w-36 flex-col items-center gap-3 rounded-xl bg-surface p-4 focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-focus tv:w-44"><span className="flex h-16 w-16 items-center justify-center rounded-full bg-accent text-on-accent text-xl tv:h-20 tv:w-20">{initials(p.display_name)}</span><span>{p.display_name}</span>{p.has_pin&&<span className="text-xs text-text-muted">Con PIN</span>}{p.id===user?.id&&<span className="text-xs text-text-muted">{user.locked?'Bloqueado':'Usuario actual'}</span>}</button></li>)}</ul>
  <div className="flex flex-wrap justify-center gap-3"><Button variant="secondary" onClick={()=>setAdding(true)}>Añadir otra cuenta</Button>{!user?.locked&&<Link to="/" className="rounded-md p-3">Volver al inicio</Link>}</div>
 </main>;
}
