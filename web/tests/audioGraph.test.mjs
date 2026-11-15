import test from 'node:test';
import assert from 'node:assert/strict';
import ts from 'typescript';
import { readFileSync } from 'node:fs';
const source = readFileSync(new URL('../src/lib/audioGraph.ts', import.meta.url), 'utf8');
const js = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { audioGraph } = await import(`data:text/javascript;base64,${Buffer.from(js).toString('base64')}`);
function context(fail = false) {
  const nodes=[];
  const node=()=>{const n={connections:[],connect(other){this.connections.push(other);return other;},disconnect(){this.connections=[];}};nodes.push(n);return n;};
  return {nodes,destination:node(),createDynamicsCompressor(){const n=node();for(const key of ['threshold','knee','ratio','attack','release'])n[key]={value:0};return n;},createGain(){return Object.assign(node(),{gain:{value:0,setTargetAtTime(value){this.value=value;}}});},createMediaElementSource(){if(fail)throw Error('unsupported');return node();}};
}
test('audio chain and volume are after compression; disposal disconnects every node',()=>{
 const c=context(),video={volume:0.4,muted:false};const graph=audioGraph(c,video,'balanced',0.4,false);
 assert.equal(c.nodes[1].threshold.value,-24);assert.equal(c.nodes[1].ratio.value,2.2);
 assert.equal(c.nodes[1].channelCount,2);assert.equal(c.nodes[2].ratio.value,20);
 assert.equal(graph.gain.gain.value,0.4);assert.equal(video.volume,1);
 graph.dispose();for(const n of c.nodes)assert.equal(n.connections.length,0);
});
test('night is stronger and muted audio never leaks at attachment',()=>{
 const c=context(),video={volume:0.7,muted:true};const g=audioGraph(c,video,'night',0.7,true);
 assert.equal(c.nodes[1].threshold.value,-32);assert.equal(c.nodes[1].ratio.value,4);assert.equal(g.gain.gain.value,0);
});
test('unsupported Web Audio leaves native sound and volume untouched',()=>{
 const video={volume:0.7,muted:true};assert.throws(()=>audioGraph(context(true),video,'balanced',0.7,true));assert.deepEqual(video,{volume:0.7,muted:true});
});

test('same element can reconnect after StrictMode cleanup without creating a second media source',()=>{
 const c=context(),video={volume:1,muted:false};let calls=0;const create=c.createMediaElementSource;c.createMediaElementSource=()=>{calls++;return create();};
 const first=audioGraph(c,video,'balanced',1,false);first.dispose();
 const second=audioGraph(c,video,'balanced',1,false);assert.equal(calls,1);second.dispose();
});

test('hot changes preserve the element, source, clock and volume',()=>{
 const c=context(),video={volume:0.5,muted:false,currentTime:1345,src:'same-file'};let calls=0;const create=c.createMediaElementSource;c.createMediaElementSource=()=>{calls++;return create();};
 const graph=audioGraph(c,video,'balanced',0.5,false);const count=c.nodes.length;
 for(const mode of ['off','night','balanced','off'])graph.setMode(mode);
 assert.equal(c.nodes.length,count);assert.equal(calls,1);assert.equal(video.currentTime,1345);assert.equal(video.src,'same-file');assert.equal(graph.gain.gain.value,0.5);
 graph.dispose();
});
