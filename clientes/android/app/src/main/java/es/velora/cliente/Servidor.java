package es.velora.cliente;

import android.content.Context;
import android.net.Uri;
import java.net.URI;
import java.net.URISyntaxException;
import java.util.Locale;
import java.util.regex.Pattern;

/**
 * Dirección del servidor de Velora. Con http:// solo se admite la red local (sin cifrar, nada debe salir de casa); con
 * https:// cualquier dirección, por ejemplo la de un proxy con certificado. Sin esquema: http en la red local y https
 * fuera de ella.
 */
final class Servidor {
    static final String POR_DEFECTO = "";
    private static final Pattern PRIVADA = Pattern.compile(
            "^(10\\.\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}|192\\.168\\.\\d{1,3}\\.\\d{1,3}|172\\.(1[6-9]|2\\d|3[01])\\.\\d{1,3}\\.\\d{1,3}|[a-z0-9-]+\\.local|[a-z0-9-]+)$");

    /** Nombre de dominio o IPv4 (sin espacios, guiones bajos ni puntos al principio o al final). */
    private static final Pattern NOMBRE = Pattern.compile(
            "^(?=.{1,253}$)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$");
    private static final Pattern LOOPBACK = Pattern.compile("^(127\\.\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}|localhost)$");

    /** Texto de ayuda de la pantalla de la dirección. */
    static final String AYUDA = "En casa, la IP del servidor (por ejemplo 192.168.1.10:8640). Desde fuera, una dirección https://.";
    /** Aviso si la dirección no vale. */
    static final String NO_VALIDA = "Esa dirección no vale: usa una de tu red local o una que empiece por https://.";

    private Servidor() {}

    static String leer(Context c) {
        return c.getSharedPreferences("velora", Context.MODE_PRIVATE).getString("servidor", POR_DEFECTO);
    }

    static void guardar(Context c, String origen) {
        c.getSharedPreferences("velora", Context.MODE_PRIVATE).edit().putString("servidor", origen).apply();
    }

    /** Devuelve el origen normalizado (esquema://host[:puerto]) o null si la dirección no se admite. */
    static String normalizar(String texto) {
        String t = texto == null ? "" : texto.trim();
        if (t.isEmpty() || t.contains(" ")) return null;
        boolean conEsquema = t.contains("://");
        URI u;
        try {
            u = new URI(conEsquema ? t : "http://" + t);
        } catch (URISyntaxException e) {
            return null;
        }
        String host = u.getHost();
        if (host == null || u.getUserInfo() != null || u.getQuery() != null || u.getFragment() != null) return null;
        // Velora va en la raíz del dominio: una ruta («https://x.com/velora») no funcionaría.
        if (u.getRawPath() != null && !u.getRawPath().isEmpty() && !"/".equals(u.getRawPath())) return null;
        host = host.toLowerCase(Locale.ROOT);
        if (!NOMBRE.matcher(host).matches()) return null;
        boolean local = esLocal(host);
        String esquema = conEsquema ? u.getScheme().toLowerCase(Locale.ROOT) : (local ? "http" : "https");
        if ("http".equals(esquema) && !local) return null; // sin cifrar, solo dentro de casa
        if (!"http".equals(esquema) && !"https".equals(esquema)) return null;
        int puerto = u.getPort();
        if (puerto == 0 || puerto > 65535) return null;
        return esquema + "://" + host + (puerto > 0 ? ":" + puerto : "");
    }

    /** Red privada, nombre .local o de una sola palabra, o el propio aparato. */
    static boolean esLocal(String host) {
        return PRIVADA.matcher(host).matches() || LOOPBACK.matcher(host).matches();
    }

    static boolean mismoOrigen(String origen, Uri destino) {
        String d = destino.getScheme() + "://" + destino.getHost() + (destino.getPort() > 0 ? ":" + destino.getPort() : "");
        return origen.equalsIgnoreCase(d);
    }
}
