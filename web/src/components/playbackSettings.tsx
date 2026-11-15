import { useEffect, useState } from 'react';
import { api } from '../lib/api';
import { Banner, Button, Card } from './ui';
interface Preferences {max_streams_per_user:number;max_streams_per_ip:number;audio_dynamics:'off'|'balanced'|'night';audio_language:string;transcoding_mode:string;transcoding_max_height:number;transcoding_bitrate_kbps:number;hardware_available?:boolean;hardware_detail?:string;active_video_transcodes?:number}
export function PlaybackSettings() {
 const [cfg,setCfg]=useState<Preferences>({max_streams_per_user:0,max_streams_per_ip:0,audio_dynamics:'balanced',audio_language:'es-ES',transcoding_mode:'auto',transcoding_max_height:1080,transcoding_bitrate_kbps:6000});
 const [ready,setReady]=useState(false),[busy,setBusy]=useState(false),[notice,setNotice]=useState('');
 useEffect(()=>{void api<Preferences>('GET','/api/v1/admin/playback').then(c=>{setCfg(c);setReady(true);}).catch(()=>setNotice('No se han podido cargar los ajustes de reproducción.'));},[]);
 const select='rounded-lg border border-border bg-surface p-3 text-text';
 return <Card><form className="flex max-w-xl flex-col gap-4" onSubmit={async e=>{e.preventDefault();setBusy(true);try {
 const {max_streams_per_user,max_streams_per_ip,audio_dynamics,audio_language,transcoding_mode,transcoding_max_height,transcoding_bitrate_kbps}=cfg;
 await api('PUT','/api/v1/admin/playback',{max_streams_per_user,max_streams_per_ip,audio_dynamics,audio_language,transcoding_mode,transcoding_max_height,transcoding_bitrate_kbps});setNotice('Ajustes guardados. Se aplican al iniciar la siguiente reproducción.');}catch {setNotice('No se han podido guardar los ajustes de reproducción.');}finally{setBusy(false);}}}>
 <h2 className="text-lg">Reproducción</h2>
 <label className="flex flex-col gap-2">Idioma de audio predeterminado
 <select className={select} value={cfg.audio_language} onChange={e=>setCfg({...cfg,audio_language:e.target.value})} disabled={!ready||busy}>
 {Object.entries({'es-ES':'Español de España','es-419':'Español de Latinoamérica','en':'Inglés','ca':'Catalán','eu':'Euskera','gl':'Gallego','fr':'Francés','de':'Alemán','it':'Italiano','pt':'Portugués','ja':'Japonés','ko':'Coreano','zh':'Chino','':'Predeterminado del archivo'}).map(([value,label])=><option key={value} value={value}>{label}</option>)}
 </select></label>
 <p className="text-sm text-text-muted">Se prioriza este idioma cuando existe. Puedes cambiar la pista manualmente. Si el archivo no distingue entre castellano y latino, se respeta la pista española predeterminada.</p>
 <h3 className="font-semibold">Reproducciones simultáneas</h3>
 <p className="text-sm text-text-muted">Activa uno o ambos límites. Sin ninguno activo no se limita la cantidad. Las reproducciones pausadas y preparando vídeo también ocupan una plaza; un dispositivo desconectado la libera en 45 segundos. Las reproducciones ya admitidas pueden terminar al reducir un límite.</p>
 {(['max_streams_per_user','max_streams_per_ip'] as const).map(field=><div key={field} className="flex items-center justify-between gap-3"><label className="flex items-center gap-2"><input type="checkbox" checked={cfg[field]>0} disabled={!ready||busy} onChange={e=>setCfg({...cfg,[field]:e.target.checked?1:0})}/>{field==='max_streams_per_user'?'Limitar por usuario':'Limitar por dirección IP'}</label>{cfg[field]>0&&<input aria-label={field==='max_streams_per_user'?'Máximo por usuario':'Máximo por IP'} className={`${select} w-24`} type="number" min={1} max={1000} required disabled={busy} value={cfg[field]} onChange={e=>setCfg({...cfg,[field]:Number(e.target.value)})}/>}</div>)}
 <p className="text-sm text-text-muted">El límite por IP se comparte entre todas las cuentas que usen esa dirección, por ejemplo, varios dispositivos de una misma casa.</p>
 <h3 className="font-semibold">Adaptación de vídeo y audio</h3>
 <p className="text-sm text-text-muted">Primero se reproduce el archivo original. Si hace falta cambiar el contenedor se copian las pistas. Solo se convierte el vídeo o el audio que el dispositivo no admite.</p>
 <label className="flex flex-col gap-2">Vídeo incompatible<select className={select} value={cfg.transcoding_mode} onChange={e=>setCfg({...cfg,transcoding_mode:e.target.value})} disabled={!ready||busy}>
 <option value="auto">Automático · preferir hardware, con respaldo por software</option><option value="software">Solo software</option><option value="off">Desactivar conversión de vídeo</option></select></label>
 {ready&&<p className="text-sm text-text-muted">{cfg.hardware_available?'Aceleración de vídeo disponible.':'Aceleración de vídeo no disponible; se puede usar software.'} {cfg.hardware_detail} Se permite una conversión de vídeo simultánea para proteger el servidor.</p>}
 <label className="flex flex-col gap-2">Resolución máxima al convertir<select className={select} value={cfg.transcoding_max_height} onChange={e=>setCfg({...cfg,transcoding_max_height:Number(e.target.value)})} disabled={!ready||busy}>{[480,720,1080,1440,2160].map(n=><option key={n} value={n}>{n}p</option>)}</select></label>
 <label className="flex flex-col gap-2">Bitrate máximo al convertir<select className={select} value={cfg.transcoding_bitrate_kbps} onChange={e=>setCfg({...cfg,transcoding_bitrate_kbps:Number(e.target.value)})} disabled={!ready||busy}>{[2000,4000,6000,8000,12000,20000].map(n=><option key={n} value={n}>{n/1000} Mb/s</option>)}</select></label>
 <p className="text-sm text-text-muted">Estos límites solo afectan al vídeo convertido. Los archivos compatibles conservan su resolución, calidad y pistas originales.</p>
 {notice&&<Banner tone="info">{notice}</Banner>}<Button type="submit" disabled={!ready||busy}>Guardar ajustes de reproducción</Button>
 </form></Card>;
}
