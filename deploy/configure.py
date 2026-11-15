#!/usr/bin/env python3
"""Create optional local settings without changing the generated database secret."""
from pathlib import Path
import os

root = Path(__file__).resolve().parent
path = root / '.env'
if path.exists():
    raise SystemExit('deploy/.env ya existe. Se conserva sin modificar.')
text = (root / '.env.example').read_text()
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as file:
    file.write(text)
(root.parent / 'media').mkdir(exist_ok=True)
(root.parent / 'dist' / 'ota').mkdir(parents=True, exist_ok=True)
print('Creado deploy/.env opcional y privado. Personaliza tus rutas; la contraseña automática se conserva en Docker.')
