# Seguridad

Velora debe instalarse con sus propias credenciales y volúmenes vacíos. No reutilices bases de datos, claves maestras, cookies, códigos o claves de firma de otra instalación.

- El acceso por HTTP se limita a redes privadas de confianza. Usa un proxy HTTPS antes de exponer la web fuera de esa red y activa las cookies Secure. El APK admite servidores de la red local por HTTP y cualquier servidor por HTTPS.
- Mantén PostgreSQL sin puertos públicos. No confíes en X-Forwarded-For salvo desde proxies expresamente configurados.
- Protege `deploy/.env`, los volúmenes config y database, los backups y la clave de firma Android. No los adjuntes a incidencias ni los añadas a Git.
- El PIN protege cada perfil frente a otros usuarios de Velora: se pide al elegir el perfil, al vincular un dispositivo a él y, según la opción de cada perfil, tras un tiempo sin uso (el servidor bloquea lo privado hasta que se escribe). No es una contraseña de acceso público ni sustituye el bloqueo del sistema operativo.
- Los restablecimientos hechos por otra persona (propietario o administrador) guardan aparte el historial del perfil, que solo devuelve su código de recuperación anterior. Esta protección es frente a usuarios a través de Velora: quien controla el servidor, la base de datos o sus copias puede leerlos directamente.
- Los códigos de vinculación y del primer asistente conceden acceso si se autorizan; no los publiques. La autorización requiere una sesión con permisos y confirmación del dispositivo.
- Usa solo medios y fuentes a los que tengas derecho de acceso. Las integraciones externas tienen sus propias condiciones.

Para informar de una posible vulnerabilidad, no publiques credenciales ni una explotación contra servidores reales. Si el repositorio dispone de un canal privado de avisos, úsalo. Si no lo tiene, abre una incidencia mínima solicitando un canal de contacto, sin incluir detalles explotables. El proyecto no ofrece SLA ni una auditoría de seguridad externa.
