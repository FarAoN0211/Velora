# 7. Problemas frecuentes

**«Aún no hay nada que ver».** Tu cuenta no tiene bibliotecas asignadas. Pídeselo al administrador (Panel de control →
Usuarios). Los miembros de un hogar ven las bibliotecas del propietario.

**Un título está mal identificado o le faltan imágenes.** El administrador puede usar **Identificar…** o **Editar
imágenes…** desde su menú. Comprueba que TMDB está configurado en Metadatos.

**El código de seis dígitos no funciona.** Caduca a los cinco minutos y solo vale una vez: genera otro en el dispositivo.
Comprueba que lo autorizas en el mismo servidor.

**He olvidado el PIN.** Usa **He olvidado mi PIN** con tu código de recuperación. Sin código, pide al propietario del hogar
que lo restablezca (tu historial se guardará aparte 30 días y lo recuperas con el código anterior si lo encuentras).
Ver [capítulo 4](04-pin-bloqueo-privacidad.md).

**«Demasiados intentos. Espera unos minutos.»** Se han fallado varias veces el PIN o el código. Espera y vuelve a
intentarlo con calma; al entrar verás cuántos intentos fallidos hubo.

**Me pide el PIN muy a menudo.** Cambia **Cuándo se vuelve a pedir el PIN** en Mi cuenta / Mi perfil. Con «Siempre» se
pide también al volver tras 2 minutos fuera de la app.

**No puedo añadir un miembro.** Has llegado al máximo de perfiles o el administrador ha retirado el permiso de hogar:
**Mi hogar** indica el motivo. Puedes eliminar un miembro (va a la papelera y deja hueco) o pedir al administrador que
amplíe el límite.

**No puedo recuperar un miembro de la papelera.** Hace falta un hueco libre y el permiso de hogar. Si han pasado más de
30 días, ya se ha eliminado.

**«Se ha alcanzado el límite de reproducciones simultáneas.»** Hay tantas reproducciones activas en tu hogar (o en tu IP)
como permite el servidor. Detén alguna o pide al administrador que cambie el límite.

**El vídeo no se reproduce en la TV.** Algunos aparatos no decodifican ciertos formatos (por ejemplo, HDR o algunos
códecs 4K). Prueba otra versión del título o reprodúcelo en otro aparato. El administrador puede revisar en
**Reproduciendo ahora** si se está convirtiendo y con qué calidad.

**La app no conecta con el servidor.** Comprueba la dirección (red local por HTTP o HTTPS válido), que el servidor está
en marcha (`curl -f http://IP_DE_TU_SERVIDOR:8640/healthz`) y que estás en tu red o en tu VPN.

**Antes de pedir ayuda en público**, quita de registros y capturas: direcciones, usuarios, códigos, QR, cookies, tokens y
claves. Nunca adjuntes `deploy/.env`, el volumen `config` ni copias de la base de datos.
