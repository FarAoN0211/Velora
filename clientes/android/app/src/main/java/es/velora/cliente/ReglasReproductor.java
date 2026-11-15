package es.velora.cliente;

import java.util.ArrayList;
import java.util.Collection;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

/** Las reglas del reproductor propio, sin nada de Android: así se pueden probar sin aparato. Quien las usa es {@link ReproductorActivity}. */
final class ReglasReproductor {
    static String subtituloForzadoEspanol(List<JSONObject> pistas) {
        JSONObject primero=null;
        for(JSONObject p:pistas) {
            String idioma=p.optString("language", "").toLowerCase(Locale.ROOT).replace('_','-');
            if(!p.optBoolean("forced") || !(idioma.equals("es")||idioma.equals("spa")||idioma.startsWith("es-")||idioma.startsWith("spa-")))continue;
            if(primero==null || (p.optBoolean("supported")&&!primero.optBoolean("supported")) || (p.optBoolean("supported")==primero.optBoolean("supported")&&p.optBoolean("default")))primero=p;
        }
        return primero==null ? "" : primero.optString("id");
    }

    /** E2: sin créditos marcados, el botón «Siguiente episodio» aparece este tiempo antes del final (como en la web). */
    static final long ANTES_DEL_FINAL_MS = 30_000;

    private static final Map<String, String> VIDEO = new HashMap<>();
    private static final Map<String, String> AUDIO = new HashMap<>();
    private static final Map<String, String> IDIOMAS = new HashMap<>();

    static {
        VIDEO.put("video/avc", "h264");
        VIDEO.put("video/mp4v-es", "mpeg4");
        VIDEO.put("video/mpeg2", "mpeg2video");
        VIDEO.put("video/x-vnd.on2.vp8", "vp8");
        VIDEO.put("video/hevc", "hevc");
        VIDEO.put("video/x-vnd.on2.vp9", "vp9");
        VIDEO.put("video/av01", "av1");
        AUDIO.put("audio/mp4a-latm", "aac");
        AUDIO.put("audio/mpeg", "mp3");
        AUDIO.put("audio/opus", "opus");
        AUDIO.put("audio/flac", "flac");
        AUDIO.put("audio/vorbis", "vorbis");
        AUDIO.put("audio/ac3", "ac3");
        AUDIO.put("audio/eac3", "eac3");
        AUDIO.put("audio/vnd.dts", "dts");
        AUDIO.put("audio/true-hd", "truehd");
        String[][] idiomas = {{"spa", "Español"}, {"es", "Español"}, {"eng", "Inglés"}, {"en", "Inglés"}, {"fre", "Francés"}, {"fra", "Francés"}, {"fr", "Francés"},
            {"ger", "Alemán"}, {"deu", "Alemán"}, {"de", "Alemán"}, {"ita", "Italiano"}, {"it", "Italiano"}, {"por", "Portugués"}, {"pt", "Portugués"},
            {"cat", "Catalán"}, {"ca", "Catalán"}, {"jpn", "Japonés"}, {"ja", "Japonés"}, {"kor", "Coreano"}, {"ko", "Coreano"}, {"chi", "Chino"}, {"zho", "Chino"},
            {"zh", "Chino"}, {"rus", "Ruso"}, {"ru", "Ruso"}};
        for (String[] i : idiomas) IDIOMAS.put(i[0], i[1]);
    }

    private ReglasReproductor() {
    }

    /** Lo que este aparato declara al servidor. Media3 abre estos formatos de archivo por sí mismo, sin reempaquetar. */
    static JSONObject caps(Collection<String> tiposDecodificables, Collection<String> audioDirecto) throws JSONException {
        List<String> video = new ArrayList<>();
        List<String> audio = new ArrayList<>(audioDirecto);
        for (String tipo : tiposDecodificables) {
            String t = tipo.toLowerCase(Locale.ROOT);
            if (VIDEO.containsKey(t) && !video.contains(VIDEO.get(t))) video.add(VIDEO.get(t));
            if (AUDIO.containsKey(t) && !audio.contains(AUDIO.get(t))) audio.add(AUDIO.get(t));
        }
        JSONObject caps = new JSONObject();
        caps.put("containers", new JSONArray(new String[] {"mp4", "mkv", "webm", "ts"}));
        caps.put("video", new JSONArray(video));
        caps.put("select_audio", true);
        caps.put("video_transcode", true);
        caps.put("audio", new JSONArray(audio));
        return caps;
    }

    /** Igual, y además la mayor altura que decodifica el aparato para cada códec de vídeo (tipo MIME → líneas). */
    static JSONObject caps(Collection<String> tiposDecodificables, Collection<String> audioDirecto, Map<String, Integer> altoPorTipo) throws JSONException {
        JSONObject caps = caps(tiposDecodificables, audioDirecto);
        JSONObject altos = new JSONObject();
        for (Map.Entry<String, Integer> e : altoPorTipo.entrySet()) {
            String codec = VIDEO.get(e.getKey().toLowerCase(Locale.ROOT));
            if (codec != null && e.getValue() != null && e.getValue() > 0) altos.put(codec, Math.max(altos.optInt(codec), e.getValue()));
        }
        if (altos.length() > 0) caps.put("max_heights", altos);
        return caps;
    }

    static void limites(JSONObject caps, String campo, Map<String,Integer> valores) throws JSONException {
        JSONObject limites = new JSONObject();
        for (Map.Entry<String,Integer> entrada : valores.entrySet()) {
            String codec = VIDEO.get(entrada.getKey().toLowerCase(Locale.ROOT));
            if (codec != null && entrada.getValue() != null && entrada.getValue() > 0) limites.put(codec, entrada.getValue());
        }
        if (limites.length() > 0) caps.put(campo, limites);
    }

    /** Las mismas capacidades de vídeo, pero pidiendo al servidor que entregue MP4 con AAC: el camino de respaldo si el archivo directo falla. */
    static JSONObject capsFlujo(JSONObject caps) throws JSONException {
        JSONObject flujo = new JSONObject(caps.toString());
        flujo.put("force_remux", true);
        return flujo;
    }

    /**
     * Un 423: el servidor ha bloqueado el perfil (pasó su plazo sin uso y hay que escribir el PIN). No es un fallo del vídeo:
     * se vuelve a la web, que enseña la pantalla de PIN. Ver replica/PLAN_HOGARES_Y_PIN.md, fase 4.
     */
    static boolean perfilBloqueado(int codigoHttp) {
        return codigoHttp == 423;
    }

    /** Un 401, 403 o 404: ya no hay permiso y reintentar no lo arregla. */
    static boolean sinPermiso(int codigoHttp) {
        return codigoHttp == 401 || codigoHttp == 403 || codigoHttp == 404;
    }

    /**
     * Por qué ha fallado la reproducción, según el código de error de Media3 (familias: 1xxx general, 2xxx red y lectura,
     * 3xxx formato del archivo, 4xxx decodificación, 5xxx salida de audio), la respuesta HTTP y el tipo de pista que
     * estaba decodificando. Cada causa tiene su remedio: un corte se reintenta igual, un audio o un formato que el aparato
     * no abre se reempaqueta, y un vídeo que no decodifica no se arregla reempaquetando (se copia tal cual).
     */
    enum Causa { TRANSPORTE, OCUPADO, FORMATO, VIDEO }

    static Causa causa(int codigoError, int codigoHttp, String tipoPista) {
        if (codigoHttp == 503 || codigoHttp == 429) return Causa.OCUPADO;
        if (codigoHttp == 415 || codigoHttp == 422) return Causa.VIDEO; // el servidor no puede adaptar este vídeo
        if (codigoError >= 3000 && codigoError < 4000) return Causa.FORMATO;
        if (codigoError >= 4000 && codigoError < 6000) {
            return tipoPista != null && tipoPista.startsWith("video/") ? Causa.VIDEO : Causa.FORMATO;
        }
        return Causa.TRANSPORTE;
    }

    /** Espera antes de reintentar: algo más larga si el servidor dice que está ocupado, y creciente con cada intento. */
    static long esperaMs(Causa causa, int intento) {
        long base = causa == Causa.OCUPADO ? 3000 : 1000;
        return base * Math.max(1, Math.min(intento, ReglasReproductor.Recuperacion.MAX_REINTENTOS));
    }

    /** Qué hacer cuando la reproducción falla o un flujo se corta antes de tiempo. Siempre acaba: nunca un ciclo de carga sin fin. */
    static final class Recuperacion {
        /** REINTENTAR: el mismo modo desde el mismo punto. NO_COMPATIBLE: avisar de que este aparato no decodifica el vídeo. */
        enum Accion { REINTENTAR, PEDIR_FLUJO, PEDIR_VIDEO, RESPALDO, NO_COMPATIBLE }

        static final int MAX_REINTENTOS = 3;
        /** Solo cuenta como recuperado si la película ha avanzado de verdad desde el último corte; estar «listo» un instante no basta. */
        static final long AVANCE_MS = 60000;
        private boolean flujoForzado;
        private boolean videoReintentado;
        private boolean videoForzado;
        private int reintentos;
        private long marcaMs = -1;

        Accion alFallar(boolean enFlujo, long posicionMs, Causa causa) {
            if (marcaMs >= 0 && posicionMs - marcaMs >= AVANCE_MS) reintentos = 0;
            marcaMs = posicionMs;
            switch (causa) {
                case FORMATO:
                    // Reempaquetar (MP4 con AAC) sí lo resuelve; si ya se reempaquetaba, no hay más que probar aquí.
                    if (enFlujo || flujoForzado) return Accion.RESPALDO;
                    flujoForzado = true;
                    return Accion.PEDIR_FLUJO;
                case VIDEO:
                    // Un decodificador puede fallar al arrancar por estar ocupado: un solo reintento; después, el aviso.
                    if (videoForzado || reintentos >= MAX_REINTENTOS) return Accion.NO_COMPATIBLE;
                    if (videoReintentado) {videoForzado=true;return Accion.PEDIR_VIDEO;}
                    videoReintentado = true;
                    reintentos++;
                    return Accion.REINTENTAR;
                default:
                    // Un corte o un servidor ocupado: el modo sirve, solo hay que volver a pedirlo (cambiarlo leería el mismo origen).
                    if (reintentos >= MAX_REINTENTOS) return Accion.RESPALDO;
                    reintentos++;
                    return Accion.REINTENTAR;
            }
        }

        int reintentos() {
            return reintentos;
        }

        boolean videoForzado() { return videoForzado; }

        boolean flujoForzado() {
            return flujoForzado;
        }
    }

    /** Respuesta del servidor sobre cómo reproducir un archivo. */
    static final class Plan {
        final String dinamica;
        final String metodo;
        final int audio;
        final String url;
        final long duracionMs;
        final String motivo;

        Plan(JSONObject j) {
            dinamica = j.optString("audio_dynamics", "off");
            metodo = j.optString("method", "unsupported");
            audio = j.optInt("audio_index", -1);
            url = j.optString("url", "");
            duracionMs = j.optLong("duration_ms", 0);
            JSONArray razones = j.optJSONArray("reasons");
            StringBuilder s = new StringBuilder();
            for (int i = 0; razones != null && i < razones.length(); i++) s.append(i > 0 ? " " : "").append(razones.optString(i));
            motivo = s.toString();
        }

        boolean reproducible() {
            return !"unsupported".equals(metodo) && url.startsWith("/");
        }

        /** Flujo reempaquetado por el servidor: se pide desde un segundo y no se puede saltar dentro de él. */
        boolean flujo() {
            return "direct_stream".equals(metodo) || "transcode".equals(metodo);
        }
    }

    static String urlFlujo(String url, double segundo, int intento) {
        return url + "&t=" + String.format(Locale.ROOT, "%.3f", segundo) + "&n=" + intento;
    }

    /** Dónde empezar: una película vista o un extra, desde el principio. */
    static long inicioMs(boolean visto, long posicionMs, boolean desdeInicio, boolean extra) {
        return desdeInicio || extra || visto ? 0 : Math.max(0, posicionMs);
    }

    static long limitarMs(long ms, long duracionMs) {
        long tope = duracionMs > 1000 ? duracionMs - 1000 : Long.MAX_VALUE;
        return Math.max(0, Math.min(ms, tope));
    }

    /** Un flujo cortado (por una pausa larga, por ejemplo) termina antes de tiempo: no es el final de la película. */
    static boolean finPrematuro(boolean flujo, long posicionMs, long duracionMs) {
        return flujo && duracionMs > 0 && posicionMs < duracionMs - 5000;
    }

    static String tiempo(long ms) {
        long s = Math.max(0, ms) / 1000;
        return s >= 3600 ? String.format(Locale.ROOT, "%d:%02d:%02d", s / 3600, s / 60 % 60, s % 60) : String.format(Locale.ROOT, "%d:%02d", s / 60, s % 60);
    }

    static String idioma(String codigo) {
        if (codigo == null || codigo.isEmpty() || "und".equals(codigo)) return "Idioma sin indicar";
        String nombre = IDIOMAS.get(codigo.toLowerCase(Locale.ROOT));
        return nombre != null ? nombre : codigo;
    }

    static String etiquetaAudio(String idioma, String codec, int canales, String titulo) {
        StringBuilder s = new StringBuilder(idioma(idioma));
        if (codec != null && !codec.isEmpty()) s.append(" · ").append(codec.toUpperCase(Locale.ROOT));
        if (canales == 2) s.append(" · Estéreo");
        else if (canales == 6) s.append(" · 5.1");
        else if (canales == 8) s.append(" · 7.1");
        if (titulo != null && !titulo.isEmpty()) s.append(" · ").append(titulo);
        return s.toString();
    }

    /** Desde estas pantallas, Atrás ofrece salir de la app en vez de recorrer lo visitado. */
    static boolean pantallaRaiz(String ruta) {
        return ruta == null || ruta.isEmpty() || "/".equals(ruta) || "/entrar".equals(ruta) || "/configurar".equals(ruta);
    }

    /**
     * Cuántos pasos retroceder con Atrás (negativo), o 0 si no queda ninguna pantalla anterior válida. Se salta todo vídeo (/ver/…), ya
     * terminado o cerrado, y las entradas iguales a la pantalla actual: Atrás nunca vuelve a un vídeo ni repite la misma pantalla.
     * Cada entrada es la ruta con su consulta (por ejemplo «/biblioteca/1?genero=Drama»).
     */
    static int pasosAtras(java.util.List<String> historial, int actual) {
        if (actual <= 0 || actual >= historial.size()) return 0;
        String aqui = historial.get(actual);
        for (int i = actual - 1; i >= 0; i--) {
            String r = historial.get(i);
            if (r == null || r.startsWith("/ver/") || r.equals(aqui)) continue;
            return i - actual;
        }
        return 0;
    }

    /** El valor de una cookie, o null. */
    static String cookie(String cookies, String nombre) {
        if (cookies == null) return null;
        for (String parte : cookies.split(";")) {
            String p = parte.trim();
            if (p.startsWith(nombre + "=")) return p.substring(nombre.length() + 1);
        }
        return null;
    }

    static final class Cue {
        final long inicio;
        final long fin;
        final String texto;

        Cue(long inicio, long fin, String texto) {
            this.inicio = inicio;
            this.fin = fin;
            this.texto = texto;
        }
    }

    /** Subtítulos del servidor. Van con el tiempo del archivo, también cuando el vídeo llega en un flujo que empieza más adelante. */
    static List<Cue> leerVtt(String vtt) {
        List<Cue> cues = new ArrayList<>();
        String[] lineas = vtt.replace("\r", "").split("\n");
        for (int i = 0; i < lineas.length; i++) {
            int flecha = lineas[i].indexOf("-->");
            if (flecha < 0) continue;
            long inicio = marca(lineas[i].substring(0, flecha));
            String resto = lineas[i].substring(flecha + 3).trim();
            int espacio = resto.indexOf(' ');
            long fin = marca(espacio > 0 ? resto.substring(0, espacio) : resto);
            StringBuilder texto = new StringBuilder();
            for (i++; i < lineas.length && !lineas[i].isEmpty(); i++) {
                if (texto.length() > 0) texto.append('\n');
                texto.append(lineas[i].replaceAll("<[^>]*>", ""));
            }
            if (inicio >= 0 && fin > inicio && texto.length() > 0) cues.add(new Cue(inicio, fin, texto.toString()));
        }
        return cues;
    }

    private static long marca(String t) {
        try {
            String[] partes = t.trim().replace(',', '.').split(":");
            long ms = 0;
            for (int i = 0; i < partes.length - 1; i++) ms = ms * 60 + Long.parseLong(partes[i]);
            return Math.round((ms * 60 + Double.parseDouble(partes[partes.length - 1])) * 1000);
        } catch (NumberFormatException e) {
            return -1;
        }
    }

    static String cueActiva(List<Cue> cues, long ms) {
        StringBuilder s = new StringBuilder();
        for (Cue c : cues) {
            if (ms >= c.inicio && ms < c.fin) s.append(s.length() > 0 ? "\n" : "").append(c.texto);
        }
        return s.toString();
    }
}
