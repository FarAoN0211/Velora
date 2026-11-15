import {useEffect,useState,type FormEvent} from 'react';
import {api,ApiError} from '../lib/api';
import {Banner,Button,Card,TextField} from './ui';
interface Provider {id:string;enabled:boolean;configured:boolean;has_client_key:boolean;has_pin:boolean}
const names:Record<string,string>={fanart:'Fanart.tv',tvdb:'TheTVDB',omdb:'The Open Movie Database (OMDb)'};
function ProviderForm({provider,reload}:{provider:Provider;reload:()=>Promise<void>}){
 const [key,setKey]=useState(''),[client,setClient]=useState(''),[pin,setPin]=useState('');
 const [enabled,setEnabled]=useState(provider.enabled),[busy,setBusy]=useState(false),[notice,setNotice]=useState(''),[failed,setFailed]=useState(false);
 const run=async(remove=false)=>{setBusy(true);setNotice('');try{
  await api(remove?'DELETE':'PUT',`/api/v1/admin/metadata/image-providers/${provider.id}`,remove?undefined:{enabled,api_key:key,client_key:client,pin});
  setKey('');setClient('');setPin('');if(remove)setEnabled(false);setFailed(false);setNotice(remove?'Configuración eliminada.':'Configuración guardada.');await reload();
 }catch(e){setFailed(true);setNotice(e instanceof ApiError?e.message:'No se ha podido guardar la fuente.');}finally{setBusy(false);}};
 return <form onSubmit={(e:FormEvent)=>{e.preventDefault();void run();}} className="flex max-w-xl flex-col gap-3 rounded-lg border border-border p-4">
 <h3 className="font-semibold">{names[provider.id]}</h3>
 <p className="text-sm text-text-muted">{provider.id==='fanart'?'Carteles, fondos, logos, arte transparente, pancartas, discos y miniaturas. Necesita una clave de proyecto; la clave personal es opcional.':provider.id==='tvdb'?'Imágenes de películas y series. Necesita clave de API v4 y, si el acceso de esa clave lo exige, PIN de suscriptor.':'Carteles de los títulos identificados por IMDb. No ofrece logos, fondos ni discos; la API especial de carteles de alta resolución requiere acceso de pago y no se utiliza aquí.'}</p>
 <a className="text-sm text-accent underline" href={provider.id==='fanart'?'https://fanart.tv/get-an-api-key/':provider.id==='tvdb'?'https://thetvdb.com/dashboard/account/apikey':'https://www.omdbapi.com/apikey.aspx'} target="_blank" rel="noreferrer">Obtener credenciales de {names[provider.id]}</a>
 <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={enabled} onChange={e=>setEnabled(e.target.checked)} disabled={busy}/>Activar fuente</label>
 <TextField label={provider.configured?'Clave API (guardada; deja vacío para conservarla)':'Clave API'} type="password" autoComplete="new-password" value={key} onChange={e=>setKey(e.target.value)} />
 {provider.id==='fanart'&&<TextField label={provider.has_client_key?'Clave personal opcional (guardada)':'Clave personal opcional'} type="password" autoComplete="new-password" value={client} onChange={e=>setClient(e.target.value)}/>}
 {provider.id==='tvdb'&&<TextField label={provider.has_pin?'PIN de suscriptor (guardado)':'PIN de suscriptor, si tu clave lo requiere'} type="password" autoComplete="new-password" value={pin} onChange={e=>setPin(e.target.value)}/>}
 {notice&&<Banner tone={failed?'error':'info'}>{notice}</Banner>}
 <div className="flex flex-wrap gap-2"><Button disabled={busy||(!key&&!provider.configured&&enabled)} type="submit">{enabled?'Comprobar y guardar':'Guardar'}</Button>{provider.configured&&<Button variant="ghost" type="button" disabled={busy} onClick={()=>void run(true)}>Eliminar configuración</Button>}</div>
 </form>;
}
export function ImageProviderSettings(){
 const [providers,setProviders]=useState<Provider[]>([]),[error,setError]=useState('');
 const load=async()=>{try{const r=await api<{providers:Provider[]}>('GET','/api/v1/admin/metadata/image-providers');setProviders(r.providers);setError('');}catch{setError('No se han podido cargar las fuentes de imágenes.');}};
 useEffect(()=>{void load();},[]);
 return <Card><div className="flex flex-col gap-4"><h2 className="text-lg">Fichas y carátulas · Fuentes de imágenes</h2>
 <p className="text-sm text-text-muted">TMDB utiliza la clave configurada arriba. Puedes añadir estas fuentes para ampliar la galería de «Editar imágenes» en todas las bibliotecas. El selector «Todo» combina las fuentes configuradas; cada imagen indica su procedencia. Las claves se guardan en el servidor y no se muestran de nuevo.</p>
 {error&&<Banner tone="error">{error}</Banner>}{providers.map(p=><ProviderForm key={p.id} provider={p} reload={load}/>)}
 </div></Card>;
}
