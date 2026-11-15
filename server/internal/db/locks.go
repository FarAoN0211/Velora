package db

// ImageFilesLock es el bloqueo consultivo que coordina las copias de seguridad con el borrado de archivos de imagen: la
// copia lo toma en exclusiva mientras reúne las imágenes y quien borra lo intenta en modo compartido. Si no lo consigue no
// borra nada: un archivo de más no molesta, y uno de menos dejaría la copia incompleta.
const ImageFilesLock int64 = 0x56454c4f5241 // «VELORA»
