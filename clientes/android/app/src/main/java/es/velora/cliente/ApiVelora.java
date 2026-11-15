package es.velora.cliente;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/** Peticiones de las pantallas nativas y del reproductor propio, todas con la misma sesión. Se llaman siempre fuera del hilo principal. */
final class ApiVelora {
    /** Una respuesta de error del servidor: 401 y 404 significan que ya no hay permiso, y no se debe insistir. */
    static final class ErrorApi extends IOException {
        final int estado;

        ErrorApi(int estado) {
            super("El servidor respondió " + estado);
            this.estado = estado;
        }
    }

    private final String origen;
    private final String cookies;
    private final String dispositivo;

    ApiVelora(String origen, String cookies, String dispositivo) {
        this.origen = origen;
        this.cookies = cookies == null ? "" : cookies;
        this.dispositivo = dispositivo == null ? "" : dispositivo;
    }

    /** También las usa la descarga del vídeo. */
    Map<String, String> cabeceras() {
        Map<String, String> c = new HashMap<>();
        c.put("Cookie", cookies);
        c.put("X-Velora-Client", "android_tv");
        if (!dispositivo.isEmpty()) c.put("X-Velora-Device-Id", dispositivo);
        return c;
    }

    String url(String ruta) {
        return origen + ruta;
    }

    String get(String ruta) throws IOException {
        return pedir("GET", ruta, null, null, 30000);
    }

    /** Un GET que otra acción puede cancelar (un salto nuevo, salir del reproductor) y con su propio límite de lectura. */
    String get(String ruta, Cancelable control, int lecturaMs) throws IOException {
        return pedir("GET", ruta, null, control, lecturaMs);
    }

    String enviar(String metodo, String ruta, String cuerpo) throws IOException {
        return pedir(metodo, ruta, cuerpo, null, 30000);
    }

    /** Igual, pero cancelable (salir del reproductor deja de esperar la respuesta). */
    String enviar(String metodo, String ruta, String cuerpo, Cancelable control) throws IOException {
        return pedir(metodo, ruta, cuerpo, control, 30000);
    }

    /** Una petición que se puede cancelar desde otro hilo: se cierra su conexión y la espera termina al momento con un error. */
    static final class Cancelable {
        private volatile HttpURLConnection conexion;
        private volatile boolean cancelada;

        void cancelar() {
            cancelada = true;
            HttpURLConnection c = conexion;
            if (c != null) c.disconnect();
        }

        boolean cancelada() {
            return cancelada;
        }
    }

    /**
     * Abre sesión y devuelve las cookies tal como las manda el servidor. Se pide como cliente con cookies porque la misma sesión la usan el reproductor
     * propio y la web del panel de control.
     */
    List<String> entrar(String cuerpo, String aparato) throws IOException {
        HttpURLConnection c = (HttpURLConnection) new URL(origen + "/api/v1/auth/login").openConnection();
        try {
            c.setConnectTimeout(8000);
            c.setReadTimeout(30000);
            c.setInstanceFollowRedirects(false);
            c.setRequestMethod("POST");
            c.setDoOutput(true);
            c.setRequestProperty("Content-Type", "application/json");
            c.setRequestProperty("X-Velora-Client", "web");
            c.setRequestProperty("X-Velora-Device-Name", aparato);
            if (!dispositivo.isEmpty()) c.setRequestProperty("X-Velora-Device-Id", dispositivo);
            try (OutputStream out = c.getOutputStream()) {
                out.write(cuerpo.getBytes(StandardCharsets.UTF_8));
            }
            int estado = c.getResponseCode();
            if (estado < 200 || estado >= 300) throw new ErrorApi(estado);
            List<String> cookies = new ArrayList<>();
            for (Map.Entry<String, List<String>> h : c.getHeaderFields().entrySet()) {
                if ("Set-Cookie".equalsIgnoreCase(h.getKey())) cookies.addAll(h.getValue());
            }
            if (cookies.isEmpty()) throw new IOException("El servidor no ha abierto sesión");
            return cookies;
        } finally {
            c.disconnect();
        }
    }

    interface ProgresoTexto { void recibir(String texto); }
    void getProgresivo(String ruta, Cancelable control, ProgresoTexto progreso) throws IOException {
        pedir("GET", ruta, null, control, 30000, progreso);
    }
    private String pedir(String metodo, String ruta, String cuerpo, Cancelable control, int lecturaMs) throws IOException {
        return pedir(metodo,ruta,cuerpo,control,lecturaMs,null);
    }
    private String pedir(String metodo, String ruta, String cuerpo, Cancelable control, int lecturaMs, ProgresoTexto progreso) throws IOException {
        if (control != null && control.cancelada()) throw new IOException("Petición cancelada");
        HttpURLConnection c = (HttpURLConnection) new URL(origen + ruta).openConnection();
        if (control != null) {
            control.conexion = c;
            if (control.cancelada()) { // cancelada justo mientras se creaba
                c.disconnect();
                throw new IOException("Petición cancelada");
            }
        }
        try {
            c.setConnectTimeout(8000);
            c.setReadTimeout(lecturaMs);
            c.setInstanceFollowRedirects(false);
            c.setRequestMethod(metodo);
            for (Map.Entry<String, String> h : cabeceras().entrySet()) c.setRequestProperty(h.getKey(), h.getValue());
            if (!"GET".equals(metodo)) {
                String csrf = ReglasReproductor.cookie(cookies, "velora_csrf");
                if (csrf != null) c.setRequestProperty("X-CSRF-Token", csrf);
            }
            if (cuerpo != null) {
                byte[] datos = cuerpo.getBytes(StandardCharsets.UTF_8);
                c.setDoOutput(true);
                c.setRequestProperty("Content-Type", "application/json");
                try (OutputStream out = c.getOutputStream()) {
                    out.write(datos);
                }
            }
            int estado = c.getResponseCode();
            if (estado < 200 || estado >= 300) throw new ErrorApi(estado);
            if (estado == 204) return "";
            try (InputStream in = c.getInputStream()) {
                ByteArrayOutputStream out = new ByteArrayOutputStream();
                byte[] b = new byte[8192];
                int r;
                while ((r = in.read(b)) > 0) {
                    out.write(b, 0, r);
                    if (progreso != null) {
                        String texto = out.toString("UTF-8");
                        int fin = texto.lastIndexOf("\n\n");
                        if (fin >= 0) progreso.recibir(texto.substring(0,fin+2));
                    }
                }
                if (progreso != null) progreso.recibir(out.toString("UTF-8"));
                return out.toString("UTF-8");
            }
        } finally {
            c.disconnect();
        }
    }
}
