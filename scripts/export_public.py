#!/usr/bin/env python3
"""Export only redistributable source; never copy the private repository's history.
Run from the repository: python3 scripts/export_public.py /absolute/empty/destination
"""
from pathlib import Path
import hashlib
import json
import os
import shutil
import sys

source = Path(__file__).resolve().parents[1]
if len(sys.argv) != 2:
    raise SystemExit('Uso: python3 scripts/export_public.py /ruta/destino-vacio')
target = Path(sys.argv[1]).resolve()
if target == source or source in target.parents and target.parts[-1] in ('server', 'web', 'clientes'):
    raise SystemExit('El destino no puede sustituir código del proyecto.')
if target.exists() and any(target.iterdir()):
    raise SystemExit('El destino debe estar vacío: no se sobrescribe un repositorio existente.')
target.mkdir(parents=True, exist_ok=True)
replacements = {}
if os.environ.get('VELORA_EXPORT_REPLACEMENTS'):
    replacements = json.loads(Path(os.environ['VELORA_EXPORT_REPLACEMENTS']).read_text(encoding='utf-8'))
files = [
 '.gitignore.public','README.md','LICENSE','SECURITY.md','CONTRIBUTING.md','Makefile','.dockerignore',
 'web/package.json','web/package-lock.json','web/index.html','web/tsconfig.json',
 'web/tailwind.config.cjs','web/tailwind.preset.cjs','web/vite.config.ts',
 'server/go.mod','server/go.sum','deploy/Dockerfile','deploy/compose.yaml',
 'deploy/compose.intel.yaml','deploy/compose.dev.yaml','deploy/.env.example',
 'deploy/configure.py','deploy/dev-go/Dockerfile','deploy/dev-go/smoke.sh',
 'docs/installation.md','docs/providers.md','docs/households.md','docs/licencias.md',
 'replica/design/tokens.css','replica/design/tailwind.preset.cjs',
 'clientes/android/README.md','clientes/android/build.gradle.kts','clientes/android/settings.gradle.kts',
 'clientes/android/app/build.gradle.kts','clientes/android/gradle.properties',
 'clientes/android/version.properties','clientes/android/gradlew','clientes/android/gradlew.bat',
 'clientes/android/publicar.sh','clientes/android/pruebas/publicar-test.sh',
 'scripts/export_public.py','scripts/audit_public.py','.github/workflows/ci.yml',
]
for directory in ['docs/manual','server/cmd','server/internal','web/src','web/public','web/tests','web/pruebas','clientes/android/app/src','clientes/android/gradle/wrapper']:
    for path in (source / directory).rglob('*'):
        if path.is_file():
            files.append(path.relative_to(source).as_posix())
manifest = {}
for relative in sorted(set(files)):
    path = source / relative
    if not path.is_file() or path.is_symlink():
        raise SystemExit('Archivo esperado ausente o enlace no permitido: '+relative)
    if any(part in {'.git','.gradle','node_modules','build','dist','datos','backups'} for part in Path(relative).parts):
        raise SystemExit('Ruta no publicable: '+relative)
    destination = target / relative
    destination.parent.mkdir(parents=True,exist_ok=True)
    content = path.read_bytes()
    # El código y las pruebas ya no llevan datos personales. Si alguna instalación necesita sustituir algo más, se
    # indica en un JSON FUERA del repositorio ({"texto privado": "sustituto"}) con VELORA_EXPORT_REPLACEMENTS: así
    # este script, que también se publica, no contiene ningún dato privado.
    if replacements and path.suffix.lower() not in {'.png','.webp','.jpg','.jpeg','.jar','.woff2'}:
        text = content.decode('utf-8')
        for private, public in replacements.items():
            text = text.replace(private, public)
        content = text.encode('utf-8')
    destination.write_bytes(content)
    if path.stat().st_mode & 0o111:
        destination.chmod(0o755)
    manifest[relative] = hashlib.sha256(content).hexdigest()
shutil.copyfile(source / '.gitignore.public',target / '.gitignore')
manifest['.gitignore'] = hashlib.sha256((target / '.gitignore').read_bytes()).hexdigest()
(target / 'SOURCE-MANIFEST.json').write_text(json.dumps(manifest,indent=2,sort_keys=True)+'\n')
print(f'Exportados {len(manifest)} archivos de código y documentación, sin historial Git.')
