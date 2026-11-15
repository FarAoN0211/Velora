import {readFileSync,readdirSync} from 'node:fs';
import {join} from 'node:path';
const assets=join(import.meta.dirname,'../dist/assets');
const css=readdirSync(assets).filter(name=>name.endsWith('.css')).map(name=>readFileSync(join(assets,name),'utf8')).join('\n');
const required=['.flex{display:flex}', '.fixed{position:fixed}', '.rounded-xl{', 'html.tv .tv\\:', '--color-bg'];
const missing=required.filter(rule=>!css.includes(rule));
if(/@tailwind\s/.test(css)||missing.length){
 console.error('CSS incompleto: Tailwind/PostCSS no ha generado los estilos de Velora.',missing);
 process.exit(1);
}
console.log('CSS verificado: tokens, utilidades y variante TV compilados.');
