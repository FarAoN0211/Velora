// Respuestas desordenadas: una acción superada nunca publica y sus peticiones se cortan.
// Uso (con las dependencias de la web instaladas): node pruebas/acciones.test.mjs
import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { fileURLToPath } from 'node:url';

const entrada = fileURLToPath(new URL('../src/lib/acciones.ts', import.meta.url));
const { outputFiles } = await build({ entryPoints: [entrada], bundle: true, format: 'esm', write: false, platform: 'neutral' });
const { Acciones } = await import('data:text/javascript;base64,' + Buffer.from(outputFiles[0].text).toString('base64'));

// Una petición simulada que responde cuando la prueba quiere y respeta la cancelación, como fetch.
function peticion(signal) {
  let responder;
  const p = new Promise((ok, mal) => {
    responder = ok;
    signal.addEventListener('abort', () => mal(new Error('cancelada')), { once: true });
  });
  return { p, responder };
}

// Como openAt/switchAudio: espera al servidor y publica solo si la acción sigue vigente.
async function accion(a, pet, publicado, nombre) {
  try {
    await pet.p;
  } catch {
    /* cancelada */
  }
  if (a.vigente()) publicado.push(nombre);
}

{
  // Un cambio de audio espera a decide; llega un salto, que responde antes; después responde (tarde) el cambio de audio.
  const carril = new Acciones();
  const publicado = [];
  const audio = carril.begin();
  const pa = peticion(audio.signal);
  const fa = accion(audio, pa, publicado, 'audio');
  const salto = carril.begin();
  assert.equal(audio.signal.aborted, true, 'el salto debe cortar la petición del cambio de audio');
  assert.equal(audio.vigente(), false);
  const ps = peticion(salto.signal);
  const fs = accion(salto, ps, publicado, 'salto');
  ps.responder();
  pa.responder();
  await Promise.all([fa, fs]);
  assert.deepEqual(publicado, ['salto']);
}

{
  // Saltos rápidos que responden en orden inverso: solo publica el último.
  const carril = new Acciones();
  const publicado = [];
  const acciones = [1, 2, 3, 4, 5].map((n) => {
    const a = carril.begin();
    return { a, pet: peticion(a.signal), n };
  });
  const fin = acciones.map(({ a, pet, n }) => accion(a, pet, publicado, n));
  for (const { pet } of [...acciones].reverse()) pet.responder();
  await Promise.all(fin);
  assert.deepEqual(publicado, [5]);
  assert.equal(acciones.slice(0, 4).every(({ a }) => a.signal.aborted), true, 'los saltos superados deben quedar cancelados');
}

{
  // Salir: la acción en curso se corta y no publica aunque su respuesta llegue después.
  const carril = new Acciones();
  const publicado = [];
  const a = carril.begin();
  const pet = peticion(a.signal);
  const f = accion(a, pet, publicado, 'preparar');
  carril.cancel();
  pet.responder();
  await f;
  assert.equal(a.signal.aborted, true);
  assert.deepEqual(publicado, []);
}

console.log('acciones: 3 casos correctos');
