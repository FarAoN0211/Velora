#!/usr/bin/env python3
"""Offline export audit. Reports paths/line numbers, never secret values.
Optional VELORA_AUDIT_PRIVATE_DIRS (os.pathsep separated) compares the export
against long secrets stored in an installation, without writing those secrets.
"""
from pathlib import Path
import base64
import json
import os
import re
import sys

root = Path(sys.argv[1] if len(sys.argv)>1 else '.').resolve()
failures=[]
known=set()
def extract(value):
    if isinstance(value,dict):
        for key,item in value.items():
            if re.search(r'key|token|password|secret|clave|cookie',str(key),re.I) and isinstance(item,str) and len(item)>=16:
                known.add(item.encode())
            extract(item)
    elif isinstance(value,list):
        for item in value: extract(item)
for private in os.environ.get('VELORA_AUDIT_PRIVATE_DIRS','').split(os.pathsep):
    if not private: continue
    base=Path(private)
    candidates=[base] if base.is_file() else list(base.glob('*'))
    for path in candidates:
        if not path.is_file():continue
        try:
            data=path.read_bytes()
            if len(data)>1_000_000:continue
            if path.suffix=='.json':extract(json.loads(data))
            elif path.suffix in ('.env','.properties') or path.name in ('.env','tmdb.key','master.key'):
                if path.name in ('tmdb.key','master.key'):
                    if len(data.strip())>=16:known.add(data.strip())
                    known.add(base64.b64encode(data))
                else:
                    for line in data.decode().splitlines():
                        if '=' in line and not line.lstrip().startswith('#'):
                            key,value=line.split('=',1)
                            if re.search(r'key|token|password|secret|clave|cookie|url',key,re.I) and len(value.strip())>=16:known.add(value.strip().encode())
        except (UnicodeError,ValueError,OSError):continue
patterns=[
 re.compile(rb'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----'),
 re.compile(rb'gh[pousr]_[A-Za-z0-9]{30,}'),
 re.compile(rb'github_pat_[A-Za-z0-9_]{30,}'),
 re.compile(rb'AKIA[0-9A-Z]{16}'),
 re.compile(rb'\b\d{8,12}:[A-Za-z0-9_-]{32,}\b'),
]
manifest_path=root/'SOURCE-MANIFEST.json'
if not manifest_path.is_file():failures.append('Falta SOURCE-MANIFEST.json')
else:
    import hashlib
    for relative,expected in json.loads(manifest_path.read_text()).items():
        path=root/relative
        if not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest()!=expected:failures.append('Manifest distinto: '+relative)
for path in root.rglob('*'):
    relative=path.relative_to(root)
    if '.git' in relative.parts:continue
    if path.is_symlink():failures.append('Enlace: '+str(relative));continue
    if not path.is_file():continue
    if any(part in {'entornos','.dev','.gradle','node_modules','datos','backups'} for part in relative.parts) or path.name in {'local.properties','firma.properties','.env','ESTADO_PROYECTO.md'} or path.suffix in {'.jks','.keystore','.dump','.apk','.aab','.db'}:
        failures.append('Archivo privado: '+str(relative))
    data=path.read_bytes()
    if any(secret in data for secret in known if len(secret)>=16):failures.append('Coincide con secreto privado: '+str(relative))
    for pattern in patterns:
        if pattern.search(data):failures.append('Formato de secreto detectado: '+str(relative))
# A distributable Android client must not contact a personal server by default.
android=root/'clientes/android/app/src/main/java/es/velora/cliente/Servidor.java'
if android.is_file() and 'POR_DEFECTO = ""' not in android.read_text():failures.append('Android contiene servidor predeterminado')
if failures:
    print('\n'.join(sorted(set(failures))));raise SystemExit(1)
print('Auditoría correcta: rutas, manifiesto, formatos de secretos y comparación privada sin coincidencias.')
