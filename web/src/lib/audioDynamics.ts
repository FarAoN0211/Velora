import { useEffect, useRef, useState, type RefObject } from 'react';

import { audioContext, audioGraph, type AudioDynamics } from './audioGraph';

/** Local DSP: no extra HTTP requests or server audio/video conversion. Keep native sound
 * until AudioContext really runs (HTTP LAN and autoplay restrictions included). */
export function useAudioDynamics(video: RefObject<HTMLVideoElement>, src: string, mode: AudioDynamics, volume: number, muted: boolean) {
  const graph = useRef<ReturnType<typeof audioGraph> | null>(null);
  const modeRef=useRef(mode);modeRef.current=mode;
  const reconnect=useRef<(()=>void)|null>(null);
  const level = useRef({ volume, muted });
  level.current = { volume, muted };
  const [status, setStatus] = useState('');
  useEffect(() => {
    const element = video.current;
    if (!element) return;
    let disposed = false, connecting = false, unavailable = false;
    let context: AudioContext | undefined;
    let attached: ReturnType<typeof audioGraph> | undefined;
    setStatus('Volumen automático pendiente de interacción');
    const connect = async () => {
      if (disposed || connecting || unavailable) return;
      if (attached) { void context?.resume().catch(() => undefined); return; }
      connecting = true;
      try {
        context ??= audioContext();
        const resumed = context.resume();
        if (context.state !== 'running') {
          // A suspended autoplay resume may stay pending until the first gesture.
          void resumed.then(() => { if (!disposed) void connect(); }).catch(() => undefined);
          return;
        }
        void resumed.catch(() => undefined);
        if (disposed) return;
        const { volume: v, muted: m } = level.current;
        attached = audioGraph(context, element, modeRef.current, v, m);
        graph.current = attached;
        setStatus(attached.processed ? (modeRef.current==='off'?'Sonido original':modeRef.current==='night'?'Volumen nocturno · estéreo':'Volumen equilibrado · estéreo') : 'Sonido original · ajuste automático no disponible');
      } catch {
        unavailable = true;
        if (!disposed) setStatus('Sonido original · ajuste automático no disponible');
      } finally { connecting = false; }
    };
    reconnect.current=()=>void connect();
    void connect();
    window.addEventListener('pointerdown', connect, true);
    window.addEventListener('keydown', connect, true);
    return () => {
      disposed = true;
      window.removeEventListener('pointerdown', connect, true);
      window.removeEventListener('keydown', connect, true);
      if (graph.current?.element === element) graph.current = null;
      reconnect.current=null;
      attached?.dispose();
    };
  }, [video, src]);
  useEffect(()=>{
    const current=graph.current;
    if(current){current.setMode(mode);setStatus(current.processed?(mode==='off'?'Sonido original':mode==='night'?'Volumen nocturno · estéreo':'Volumen equilibrado · estéreo'):'Sonido original · ajuste automático no disponible');}
    else reconnect.current?.();
  },[mode]);
  useEffect(() => {
    const current = graph.current;
    if (current && current.element === video.current) {
      current.gain.gain.setTargetAtTime(muted ? 0 : volume, current.gain.context.currentTime, 0.02);
      current.element.volume = 1;
      current.element.muted = false;
    } else if (video.current) {
      video.current.volume = volume;
      video.current.muted = muted;
    }
  }, [video, volume, muted, src, status]);
  return status;
}
