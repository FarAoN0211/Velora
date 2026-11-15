import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import QRCode from 'qrcode';
import { api, ApiError, type User } from '../lib/api';
import { useSession } from '../lib/session';
import { Banner, Button, Card } from './ui';
interface Pending {device_token:string;user_code:string;expires_at:string;interval:number}
export function QuickLogin({setup=false}:{setup?:boolean}) {
 const {setUser,refresh}=useSession();const navigate=useNavigate();
 const [pending,setPending]=useState<Pending|null>(null),[qr,setQr]=useState(''),[error,setError]=useState(''),[attempt,setAttempt]=useState(0),[seconds,setSeconds]=useState(300);
 const target=window.location.origin+(setup?'/configurar':'/vincular');
 useEffect(()=>{
  const abort=new AbortController();let timer:ReturnType<typeof setTimeout>|undefined;
  setPending(null);setQr('');setError('');
  const poll=async(p:Pending)=>{
   if(abort.signal.aborted)return;
   if(Date.now()>=Date.parse(p.expires_at)){setError('El código ha caducado. Genera uno nuevo.');return;}
   try{
    const r=await api<{status?:string;user?:User}>('POST','/api/v1/auth/device/poll',{device_token:p.device_token},abort.signal);
    if(abort.signal.aborted)return;
    if(r.user){setUser(r.user);await refresh();navigate('/',{replace:true});return;}
   }catch(e){if(abort.signal.aborted)return;if(e instanceof ApiError&&e.status===410){setError(e.message);return;}}
   timer=setTimeout(()=>void poll(p),2000);
  };
  void api<Pending>('POST','/api/v1/auth/device',{device_name:/VeloraTV/.test(navigator.userAgent)?'TV Box · Velora':'Dispositivo · Velora'},abort.signal).then(async p=>{
   if(abort.signal.aborted)return;setPending(p);
   const img=await QRCode.toDataURL(`${target}?codigo=${p.user_code}`,{width:240,margin:2,errorCorrectionLevel:'M'});
   if(abort.signal.aborted)return;setQr(img);timer=setTimeout(()=>void poll(p),2000);
  }).catch(e=>{if(!abort.signal.aborted)setError(e instanceof Error?e.message:'No se ha podido crear el código.');});
  return()=>{abort.abort();clearTimeout(timer);};
 },[attempt,setup,target,navigate,refresh,setUser]);
 useEffect(()=>{const tick=()=>setSeconds(Math.max(0,Math.ceil(((pending?Date.parse(pending.expires_at):Date.now()+300000)-Date.now())/1000)));tick();const t=setInterval(tick,1000);return()=>clearInterval(t);},[pending]);
 return <Card className="mx-auto flex w-full max-w-lg flex-col items-center gap-4 text-center tv:gap-2 tv:p-3">
  <h2 className="text-lg font-semibold">{setup?'Configurar desde el móvil':'Inicio rápido'}</h2>
  <p className="text-sm text-text-muted">{setup?'Escanea el QR para crear la primera cuenta y autorizar este televisor.':'Escanea el QR desde una sesión autorizada o facilita este código al administrador para dar de alta tu hogar.'}</p>
  {qr&&<img src={qr} width={240} height={240} alt="Código QR para vincular este dispositivo" className="rounded-lg tv:h-40 tv:w-40"/>}
  {pending&&<><p className="select-text text-display font-semibold tracking-[0.25em]" aria-label={`Código ${pending.user_code.split('').join(' ')}`}>{pending.user_code}</p><p className="select-text break-all text-sm">{target}</p><p className="text-xs text-text-muted">{seconds?`Válido durante ${Math.ceil(seconds/60)} min. Esperando tu autorización…`:'Código caducado'}</p></>}
  {error&&<Banner tone="error">{error}</Banner>}
  {(error||seconds===0)&&<Button onClick={()=>setAttempt(n=>n+1)}>Generar otro código</Button>}
  <p className="text-xs text-text-muted">No necesitas instalar el APK: también puedes usar el navegador del móvil.</p>
 </Card>;
}
