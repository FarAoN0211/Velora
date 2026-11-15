package es.velora.cliente;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.Arrays;
import java.util.Locale;
import java.util.Set;
import java.util.concurrent.atomic.AtomicBoolean;
import org.json.JSONException;
import org.json.JSONObject;

/** Las reglas de la actualización, sin nada de Android: así se pueden probar sin aparato. Quien las usa es {@link Actualizador}. */
final class ReglasOta {
    static final int MAX_ANUNCIO = 64 * 1024;
    /** El APK actual ronda unos pocos MB: el límite deja margen de sobra y evita que una respuesta sin fin llene el aparato. */
    static final long MAX_APK = 100L * 1024 * 1024;

    private ReglasOta() {
    }

    /** Un fallo que se puede contar al usuario tal cual. */
    static final class ErrorOta extends IOException {
        ErrorOta(String mensaje) {
            super(mensaje);
        }
    }

    static final class Anuncio {
        final int version;
        final String nombre;
        final String archivo;
        final String huella;

        private Anuncio(int version, String nombre, String archivo, String huella) {
            this.version = version;
            this.nombre = nombre;
            this.archivo = archivo;
            this.huella = huella;
        }

        boolean esMasNuevoQue(int actual) {
            return version > actual;
        }
    }

    static Anuncio leerAnuncio(String json) throws ErrorOta {
        try {
            JSONObject info = new JSONObject(json);
            int version = info.getInt("versionCode");
            String archivo = info.getString("archivo");
            String huella = info.getString("sha256");
            if (version <= 0) throw new ErrorOta("anuncio con una versión no válida");
            if (!archivo.matches("[A-Za-z0-9][A-Za-z0-9._-]*\\.apk")) throw new ErrorOta("anuncio con un archivo no válido");
            if (!huella.matches("[0-9a-fA-F]{64}")) throw new ErrorOta("anuncio con una huella no válida");
            String nombre = info.optString("versionName", "");
            return new Anuncio(version, nombre.matches("[\\p{Alnum} ._+-]{1,40}") ? nombre : "", archivo, huella);
        } catch (JSONException e) {
            throw new ErrorOta("anuncio ilegible");
        }
    }

    static byte[] leerAcotado(InputStream in, int maximo) throws IOException {
        byte[] b = new byte[maximo + 1];
        int n = 0, r;
        while (n <= maximo && (r = in.read(b, n, maximo + 1 - n)) > 0) n += r;
        if (n > maximo) throw new ErrorOta("anuncio demasiado grande");
        return Arrays.copyOf(b, n);
    }

    /** Guarda la descarga en un temporal y solo deja {@code destino} si está entera y su huella coincide. Si no, no queda nada en disco. */
    static void guardar(InputStream in, long declarado, File destino, long maximo, String huella) throws IOException {
        if (declarado > maximo) throw new ErrorOta("la descarga es demasiado grande");
        File temporal = new File(destino.getPath() + ".parte");
        boolean bien = false;
        destino.delete();
        try (OutputStream out = new FileOutputStream(temporal)) {
            MessageDigest sha = sha256();
            byte[] b = new byte[65536];
            long total = 0;
            int r;
            while ((r = in.read(b)) > 0) {
                total += r;
                if (total > maximo) throw new ErrorOta("la descarga es demasiado grande");
                out.write(b, 0, r);
                sha.update(b, 0, r);
            }
            if (declarado >= 0 && total != declarado) throw new ErrorOta("la descarga se ha cortado");
            if (!hex(sha.digest()).equalsIgnoreCase(huella)) throw new ErrorOta("la descarga está dañada");
            bien = true;
        } finally {
            if (!bien) temporal.delete();
        }
        if (!temporal.renameTo(destino)) {
            temporal.delete();
            throw new ErrorOta("no se ha podido guardar la descarga");
        }
    }

    /**
     * El APK debe ser de esta app, ser la versión anunciada, más nueva que la instalada y llevar su misma firma. Sin firmas que comparar
     * (un Android antiguo no informa de la del archivo) decide el propio Android al instalar, que rechaza cualquier otra.
     */
    static void comprobarApk(String paquete, String paqueteApk, int anunciada, int versionApk, int actual, Set<String> firmasApk, Set<String> firmasApp) throws ErrorOta {
        if (paqueteApk == null || !paqueteApk.equals(paquete)) throw new ErrorOta("el archivo no es Velora");
        if (versionApk != anunciada || versionApk <= actual) throw new ErrorOta("versión no válida");
        if (!firmasApk.isEmpty() && !firmasApk.equals(firmasApp)) throw new ErrorOta("la firma no coincide");
    }

    static String hex(byte[] bytes) {
        StringBuilder s = new StringBuilder();
        for (byte x : bytes) s.append(String.format(Locale.ROOT, "%02x", x));
        return s.toString();
    }

    static MessageDigest sha256() throws IOException {
        try {
            return MessageDigest.getInstance("SHA-256");
        } catch (NoSuchAlgorithmException e) {
            throw new IOException(e);
        }
    }

    /** Una sola operación de actualización a la vez, se compruebe desde donde se compruebe. */
    static final class Turno {
        private final AtomicBoolean ocupado = new AtomicBoolean();

        boolean tomar() {
            return ocupado.compareAndSet(false, true);
        }

        void soltar() {
            ocupado.set(false);
        }
    }

    /** Cuándo volver a mirar. Un fallo se reintenta antes: no debe dejar el aparato seis horas sin novedades por un corte de red. */
    static final class Plazo {
        static final long CADA = 6 * 3600 * 1000L;
        static final long REINTENTO = 15 * 60 * 1000L;
        private volatile long siguiente = Long.MIN_VALUE;

        boolean toca(long ahora) {
            return ahora >= siguiente;
        }

        void acierto(long ahora) {
            siguiente = ahora + CADA;
        }

        void fallo(long ahora) {
            siguiente = ahora + REINTENTO;
        }
    }
}
