#!/usr/bin/env python3
"""Create local deployment settings without reusing another installation's keys."""
from pathlib import Path
import os
import secrets

root = Path(__file__).resolve().parent
path = root / '.env'
if path.exists():
    raise SystemExit('deploy/.env ya existe. Se conserva sin modificar.')
text = (root / '.env.example').read_text().replace('GENERATE_WITH_CONFIGURE_PY', secrets.token_hex(32))
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as file:
    file.write(text)
(root.parent / 'media').mkdir(exist_ok=True)
(root.parent / 'dist' / 'ota').mkdir(parents=True, exist_ok=True)
print('Creado deploy/.env privado. Ajusta VELORA_MEDIA_PATH antes de iniciar Velora.')
