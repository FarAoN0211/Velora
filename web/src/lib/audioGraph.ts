export type AudioDynamics = 'off' | 'balanced' | 'night';
let shared: AudioContext | undefined;
const sources = new WeakMap<HTMLMediaElement, MediaElementAudioSourceNode>();
export function audioContext(): AudioContext {
  if (!shared || shared.state === 'closed') shared = new AudioContext();
  return shared;
}
if (typeof window !== 'undefined') {
  const unlock = () => { try { void audioContext().resume().catch(() => undefined); } catch { /* Native audio remains available. */ } };
  window.addEventListener('pointerdown', unlock, true);
  window.addEventListener('keydown', unlock, true);
}

/** Two local paths allow Original/balanced/night changes without replacing the
 * media element, changing its URL, seeking or reconnecting to the server. */
export function audioGraph(context: AudioContext, element: HTMLMediaElement, mode: AudioDynamics, volume: number, muted: boolean) {
  const compressor = context.createDynamicsCompressor();
  compressor.knee.value = 12;
  compressor.attack.value = 0.01;
  compressor.release.value = 0.5;
  compressor.channelCount = 2;
  compressor.channelCountMode = 'explicit';
  const limiter = context.createDynamicsCompressor();
  limiter.threshold.value = -1;
  limiter.knee.value = 0;
  limiter.ratio.value = 20;
  limiter.attack.value = 0;
  limiter.release.value = 0.08;
  const wet=context.createGain(),dry=context.createGain(),gain=context.createGain();
  wet.gain.value=mode==='off'?0:1;dry.gain.value=mode==='off'?1:0;
  gain.gain.value=muted?0:volume;
  compressor.connect(limiter).connect(wet).connect(gain).connect(context.destination);
  dry.connect(gain);
  let source=sources.get(element);
  try{if(!source){source=context.createMediaElementSource(element);sources.set(element,source);}}
  catch(error){for(const n of [compressor,limiter,wet,dry,gain])n.disconnect();throw error;}
  source.disconnect();
  let processed=true;
  try{source.connect(compressor);source.connect(dry);}catch{
    processed=false;source.disconnect();source.connect(gain);
    compressor.disconnect();limiter.disconnect();wet.disconnect();dry.disconnect();
  }
  const setMode=(next:AudioDynamics)=>{
    compressor.threshold.value=next==='night'?-32:-24;
    compressor.ratio.value=next==='night'?4:2.2;
    wet.gain.setTargetAtTime(next==='off'?0:0.95,context.currentTime,0.015);
    dry.gain.setTargetAtTime(next==='off'?1:0,context.currentTime,0.015);
  };
  setMode(mode);element.volume=1;element.muted=false;
  return {element,gain,processed,setMode,dispose:()=>{for(const n of [source,compressor,limiter,wet,dry,gain])n.disconnect();}};
}
