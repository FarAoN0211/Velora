import { useCallback, useEffect, useState } from 'react';
import { api, ApiError } from '../lib/api';
import { Button, Banner } from './ui';
import { ProgressBar } from './scanStatus';
interface Status {total:number; done:number; pending:number; intros:number; credits:number; paused:boolean; playback:boolean; running:boolean}
export function MarkerScan(){
 const [status,setStatus]=useState<Status|null>(null),[error,setError]=useState(''),[busy,setBusy]=useState(false);
 const load=useCallback(async()=>{try{setStatus(await api<Status>('GET','/api/v1/admin/markers'));setError('');}catch(e){setError(e instanceof ApiError?e.message:'No se ha podido consultar la detección.');}},[]);
 useEffect(()=>{void load();const t=window.setInterval(()=>void load(),4000);return()=>clearInterval(t);},[load]);
 async function control(method:'POST'|'DELETE'){setBusy(true);try{setStatus(await api<Status>(method,'/api/v1/admin/markers'));setError('');}catch(e){setError(e instanceof ApiError?e.message:'No se ha podido cambiar la detección.');}finally{setBusy(false);}}
 const percent=status?.total?Math.round(status.done/status.total*100):0;
 return <section aria-label="Detección de intros y créditos" className="space-y-3 rounded-lg bg-surface p-4">
  <h2 className="font-semibold">Intros y créditos</h2>
  {error&&<Banner tone="error">{error}</Banner>}
  {status&&<>
   <p className="text-sm">{status.done.toLocaleString('es-ES')} de {status.total.toLocaleString('es-ES')} episodios analizados · {status.pending.toLocaleString('es-ES')} pendientes</p>
   <ProgressBar percent={percent} label="Progreso de detección de intros y créditos" />
   <p className="text-sm text-text-muted">{status.intros.toLocaleString('es-ES')} intros y {status.credits.toLocaleString('es-ES')} marcas de créditos guardadas.</p>
   <p role="status" className="text-sm">{status.paused?'Detenido manualmente.':status.playback&&status.pending?'En espera mientras hay reproducción.':status.running?'Analizando episodios…':status.pending?'Pendiente de análisis.':'Análisis al día.'}</p>
   <div className="flex flex-wrap gap-3"><Button variant="secondary" disabled={busy||(!status.paused&&status.running)} onClick={()=>void control('POST')}>{status.paused?'Reanudar detección':'Iniciar detección'}</Button><Button variant="secondary" disabled={busy||status.paused} onClick={()=>void control('DELETE')}>Detener detección</Button></div>
   <p className="text-xs text-text-muted">Las nuevas series se analizan automáticamente. Detener conserva las marcas; reanudar continúa con los pendientes. El análisis espera durante la reproducción para evitar cortes. Algunas temporadas necesitan al menos dos episodios para comparar.</p>
  </>}
 </section>;
}
