package es.velora.cliente;

import java.io.UnsupportedEncodingException;
import java.net.URLEncoder;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

/** La interfaz nativa de TV sin Android: datos de la API, rutas, textos y normas, para poder probarlos en el ordenador. */
final class ReglasTv {
    /** Dos cierres seguidos al arrancar la pantalla nativa: la app vuelve a la web, que también comprueba actualizaciones. */
    static final int CIERRES_MAXIMOS = 2;
    static final int PAGINA = 60;

    /** Los criterios de la biblioteca, los mismos que en la web. */
    static final String[][] ORDENES = {
        {"title", "Título", "A → Z", "Z → A"},
        {"added", "Añadidas recientemente", "Más recientes", "Más antiguas"},
        {"year", "Año de estreno", "Más recientes", "Más antiguas"},
        {"rating", "Puntuación", "Mejor puntuadas", "Peor puntuadas"},
        {"runtime", "Duración", "Más largas", "Más cortas"},
        {"last_played", "Vistas recientemente", "Más recientes", "Más antiguas"},
        {"resolution", "Calidad de imagen", "Mayor calidad", "Menor calidad"},
    };
    static final String[][] ESTADOS = {{"", "Todas"}, {"unplayed", "No vistas"}, {"played", "Vistas"}};

    private ReglasTv() {}

    static class Tarjeta {
        String id = "";
        String titulo = "";
        String rev = "";
        String trailer = "";
        String calidad = "";
        String sinopsis = "";
        String clasificacion = "";
        int anio;
        long posicionMs;
        long duracionMs;
        boolean vista;
        boolean poster;
        boolean fondo;
        boolean banner;
        /** E2: movie, series, season o episode. */
        String tipo = "movie";
        String serie = "";
        int temporada = -1;

        boolean reanudable() {
            return posicionMs > 0 && !vista;
        }

        boolean esSerie() {
            return "series".equals(tipo);
        }

        /** Las series, sus temporadas y sus episodios se abren en la ficha web (S06), que ya se adapta a la TV; los
         * episodios se siguen reproduciendo con el reproductor propio. null: la ficha nativa (películas). */
        String fichaWeb() {
            if (esSerie()) return "/serie/" + cifrar(id);
            if (("episode".equals(tipo) || "season".equals(tipo)) && !serie.isEmpty())
                return "/serie/" + cifrar(serie) + (temporada >= 0 ? "?temporada=" + temporada : "");
            return null;
        }

        /** La imagen ancha de la fila «Continuar viendo» y de la cabecera; sin ella, la carátula. */
        String imagenAncha() {
            if (banner) return imagen(id, "banner", rev);
            if (fondo) return imagen(id, "backdrop", rev);
            return poster ? imagen(id, "poster", rev) : null;
        }
    }

    static final class Biblioteca {
        String id = "";
        String nombre = "";
        String tipo = "movies";
    }

    static final class Fila {
        String titulo = "";
        final List<Tarjeta> items = new ArrayList<>();
    }

    static final class Inicio {
        final List<Tarjeta> destacadas = new ArrayList<>();
        final List<Tarjeta> continuar = new ArrayList<>();
        final List<Fila> filas = new ArrayList<>();
    }

    static final class Pista {
        int indice;
        String idioma = "";
        String titulo = "";
        String codec = "";
        int canales;
        int alto;
    }

    static final class Archivo {
        String id = "";
        String version = "";
        boolean falta;
        final List<Pista> audios = new ArrayList<>();
        int alto;
    }

    static final class Extra {
        String id = "";
        String nombre = "";
        boolean esTrailer;
    }

    static final class Ficha extends Tarjeta {
        JSONObject informacion;
        boolean informacionPedida;
        String lema = "";
        final List<String> generos = new ArrayList<>();
        final List<Archivo> archivos = new ArrayList<>();
        final List<Extra> extras = new ArrayList<>();
    }

    static final class Perfil {
        String id = "";
        String nombre = "";
    }

    static final class Usuario {
        String nombre = "";
        boolean admin;
    }

    /** Atrás recorre las pantallas de la app: el reproductor es otra actividad que ya terminó y nunca entra aquí. */
    static final class Historial<T> {
        private final ArrayDeque<T> pila = new ArrayDeque<>();

        Historial(T raiz) {
            pila.push(raiz);
        }

        /** Las secciones del lateral no se apilan: desde cualquiera de ellas, Atrás lleva al inicio. */
        void seccion(T s) {
            while (pila.size() > 1) pila.pop();
            if (s != pila.peek()) pila.push(s);
        }

        void abrir(T p) {
            pila.push(p);
        }

        T actual() {
            return pila.peek();
        }

        /** La pantalla anterior, o null si ya se está en la raíz (toca ofrecer salir). */
        T atras() {
            if (pila.size() <= 1) return null;
            pila.pop();
            return pila.peek();
        }
    }

    static boolean nativaPermitida(int cierres, int versionCierres, int versionActual) {
        // Una versión nueva merece otra oportunidad: puede ser justo el arreglo.
        return versionCierres != versionActual || cierres < CIERRES_MAXIMOS;
    }

    static String imagen(String id, String tipo, String rev) {
        return "/api/v1/items/" + cifrar(id) + "/images/" + tipo + (rev == null || rev.isEmpty() ? "" : "?v=" + cifrar(rev));
    }

    static String rutaBiblioteca(String biblioteca, int desde, String orden, boolean inverso, String genero, String estado) {
        StringBuilder r = new StringBuilder("/api/v1/items?library_id=").append(cifrar(biblioteca))
                .append("&limit=").append(PAGINA).append("&offset=").append(desde).append("&sort=").append(cifrar(orden));
        if (inverso) r.append("&order=reverse");
        if (genero != null && !genero.isEmpty()) r.append("&genre=").append(cifrar(genero));
        if (estado != null && !estado.isEmpty()) r.append("&played=").append(cifrar(estado));
        return r.toString();
    }

    static String rutaBuscar(String consulta) {
        return "/api/v1/items?limit=" + PAGINA + "&q=" + cifrar(consulta.trim());
    }

    static String cifrar(String v) {
        try {
            return URLEncoder.encode(v, "UTF-8").replace("+", "%20");
        } catch (UnsupportedEncodingException imposible) {
            throw new IllegalStateException(imposible);
        }
    }

    static String datosReproductor(String item, String archivo, int audio, boolean desdeInicio, String aparato) throws JSONException {
        return new JSONObject().put("item", item).put("file", archivo == null ? "" : archivo).put("audio", audio)
                .put("inicio", desdeInicio).put("device", aparato).toString();
    }

    static String credenciales(String usuarioId, String usuario, String clave) throws JSONException {
        JSONObject j = new JSONObject().put("password", clave);
        return (usuarioId != null ? j.put("user_id", usuarioId) : j.put("username", usuario.trim())).toString();
    }

    static String duracion(long ms) {
        long min = Math.round(ms / 60000.0);
        if (min < 60) return min + " min";
        return min / 60 + " h" + (min % 60 == 0 ? "" : " " + min % 60 + " min");
    }

    static String restante(Tarjeta t) {
        return t.duracionMs > t.posicionMs ? "Quedan " + duracion(t.duracionMs - t.posicionMs) : "";
    }

    /** Fracción vista, de 0 a 1, para la barra de progreso de la carátula. */
    static float progreso(Tarjeta t) {
        if (t.duracionMs <= 0 || t.posicionMs <= 0 || t.vista) return 0;
        return Math.min(1f, (float) t.posicionMs / t.duracionMs);
    }

    static String datosBasicos(Ficha f) {
        List<String> p = new ArrayList<>();
        if (f.anio > 0) p.add(String.valueOf(f.anio));
        if (f.duracionMs > 0) p.add(duracion(f.duracionMs));
        if (!f.clasificacion.isEmpty()) p.add(f.clasificacion);
        if (!f.calidad.isEmpty()) p.add(f.calidad);
        if (!f.generos.isEmpty()) p.add(unir(f.generos, ", "));
        return unir(p, " · ");
    }

    static String nombreAudio(Pista p) {
        List<String> n = new ArrayList<>();
        n.add(p.titulo.isEmpty() ? (p.idioma.isEmpty() ? "Pista " + (p.indice + 1) : p.idioma) : p.titulo);
        if (!p.codec.isEmpty()) n.add(p.codec.toUpperCase());
        if (p.canales > 0) n.add(p.canales + " canales");
        return unir(n, " · ");
    }

    static String nombreVersion(Archivo a, int posicion) {
        if (!a.version.isEmpty()) return a.version;
        return a.alto > 0 ? a.alto + "p" : "Versión " + (posicion + 1);
    }

    private static String unir(List<String> partes, String separador) {
        StringBuilder r = new StringBuilder();
        for (String p : partes) {
            if (r.length() > 0) r.append(separador);
            r.append(p);
        }
        return r.toString();
    }

    /** El archivo que se reproduce por defecto: el primero que siga en disco. */
    static Archivo archivoInicial(Ficha f) {
        for (Archivo a : f.archivos) if (!a.falta) return a;
        return f.archivos.isEmpty() ? null : f.archivos.get(0);
    }

    static String trailer(Ficha f) {
        for (Extra e : f.extras) if (e.esTrailer) return e.id;
        return null;
    }

    static List<Biblioteca> leerBibliotecas(String json) throws JSONException {
        List<Biblioteca> r = new ArrayList<>();
        JSONArray a = new JSONObject(json).optJSONArray("libraries");
        for (int i = 0; a != null && i < a.length(); i++) {
            JSONObject b = a.getJSONObject(i);
            if (!b.optBoolean("in_menu", true)) continue;
            Biblioteca l = new Biblioteca();
            l.id = b.getString("id");
            l.nombre = b.optString("name");
            l.tipo = b.optString("kind", "movies");
            r.add(l);
        }
        return r;
    }

    static Inicio leerInicio(String json) throws JSONException {
        JSONObject j = new JSONObject(json);
        Inicio r = new Inicio();
        r.destacadas.addAll(tarjetas(j.optJSONArray("featured")));
        r.continuar.addAll(tarjetas(j.optJSONArray("continue")));
        Fila siguiente = new Fila(); // E2: «Siguiente episodio» de las series empezadas, antes de las novedades
        siguiente.titulo = "Siguiente episodio";
        siguiente.items.addAll(tarjetas(j.optJSONArray("next_up")));
        if (!siguiente.items.isEmpty()) r.filas.add(siguiente);
        JSONArray filas = j.optJSONArray("rows");
        for (int i = 0; filas != null && i < filas.length(); i++) {
            JSONObject o = filas.getJSONObject(i);
            Fila f = new Fila();
            JSONObject lib = o.optJSONObject("library");
            f.titulo = lib == null ? "" : lib.optString("name");
            f.items.addAll(tarjetas(o.optJSONArray("items")));
            if (!f.items.isEmpty()) r.filas.add(f);
        }
        return r;
    }

    static List<Tarjeta> leerItems(String json) throws JSONException {
        return tarjetas(new JSONObject(json).optJSONArray("items"));
    }

    static Ficha leerFicha(String json) throws JSONException {
        JSONObject o = new JSONObject(json).getJSONObject("item");
        Ficha f = new Ficha();
        rellenar(f, o);
        f.informacion = o;
        f.lema = texto(o, "tagline");
        JSONArray g = o.optJSONArray("genres");
        for (int i = 0; g != null && i < g.length(); i++) f.generos.add(g.getString(i));
        JSONArray a = o.optJSONArray("files");
        for (int i = 0; a != null && i < a.length(); i++) f.archivos.add(archivo(a.getJSONObject(i)));
        JSONArray x = o.optJSONArray("extras");
        for (int i = 0; x != null && i < x.length(); i++) {
            JSONObject e = x.getJSONObject(i);
            Extra extra = new Extra();
            extra.id = e.getString("id");
            extra.esTrailer = "trailer".equals(e.optString("kind"));
            extra.nombre = texto(e, "title").isEmpty() ? nombreExtra(e.optString("kind")) : texto(e, "title");
            f.extras.add(extra);
        }
        return f;
    }

    static List<Perfil> leerPerfiles(String json) throws JSONException {
        List<Perfil> r = new ArrayList<>();
        JSONArray a = new JSONObject(json).optJSONArray("profiles");
        for (int i = 0; a != null && i < a.length(); i++) {
            Perfil p = new Perfil();
            p.id = a.getJSONObject(i).getString("id");
            p.nombre = a.getJSONObject(i).optString("display_name");
            r.add(p);
        }
        return r;
    }

    static Usuario leerUsuario(String json) throws JSONException {
        JSONObject u = new JSONObject(json).getJSONObject("user");
        Usuario r = new Usuario();
        r.nombre = u.optString("display_name");
        r.admin = u.optBoolean("is_admin");
        return r;
    }

    static List<String> leerGeneros(String json) throws JSONException {
        List<String> r = new ArrayList<>();
        JSONArray a = new JSONObject(json).optJSONArray("genres");
        for (int i = 0; a != null && i < a.length(); i++) r.add(a.getJSONObject(i).getString("name"));
        return r;
    }

    static boolean requiereConfigurar(String json) throws JSONException {
        return new JSONObject(json).optBoolean("setup_required");
    }

    /** El servidor manda null en una lista vacía y en los campos sin dato. */
    private static List<Tarjeta> tarjetas(JSONArray a) throws JSONException {
        List<Tarjeta> r = new ArrayList<>();
        for (int i = 0; a != null && i < a.length(); i++) {
            Tarjeta t = new Tarjeta();
            rellenar(t, a.getJSONObject(i));
            r.add(t);
        }
        return r;
    }

    private static void rellenar(Tarjeta t, JSONObject o) throws JSONException {
        t.id = o.getString("id");
        t.titulo = o.optString("title");
        t.rev = texto(o, "image_rev");
        t.trailer = texto(o, "trailer_id");
        t.calidad = texto(o, "quality");
        t.sinopsis = texto(o, "overview");
        t.clasificacion = texto(o, "official_rating");
        t.anio = o.optInt("year");
        t.posicionMs = o.optLong("position_ms");
        t.duracionMs = o.optLong("runtime_ms");
        t.vista = o.optBoolean("played");
        t.poster = o.optBoolean("has_poster");
        t.fondo = o.optBoolean("has_backdrop");
        t.banner = o.optBoolean("has_banner");
        t.tipo = o.optString("kind", "movie");
        t.serie = texto(o, "series_id");
        t.temporada = o.has("season_number") && !o.isNull("season_number") ? o.optInt("season_number") : -1;
        String serie = texto(o, "series_title");
        if ("episode".equals(t.tipo) && !serie.isEmpty()) t.titulo = serie + " · " + codigoEpisodio(o) + " · " + t.titulo;
    }

    /** «T1 · E3», «T1 · E1–E2» o «Especiales · E1», como en la web. */
    static String codigoEpisodio(JSONObject o) {
        int t = o.optInt("season_number", -1);
        String r = t == 0 ? "Especiales" : "T" + (t < 0 ? "?" : String.valueOf(t));
        int n = o.optInt("episode_number", 0);
        if (n <= 0) return r;
        int fin = o.optInt("episode_end", 0);
        return r + " · E" + n + (fin > 0 ? "–E" + fin : "");
    }

    private static Archivo archivo(JSONObject o) throws JSONException {
        Archivo a = new Archivo();
        a.id = o.getString("id");
        a.version = texto(o, "version_label");
        a.falta = o.optBoolean("missing");
        JSONArray s = o.optJSONArray("streams");
        for (int i = 0; s != null && i < s.length(); i++) {
            JSONObject p = s.getJSONObject(i);
            if ("video".equals(p.optString("kind"))) a.alto = Math.max(a.alto, p.optInt("height"));
            if (!"audio".equals(p.optString("kind"))) continue;
            Pista pista = new Pista();
            pista.indice = p.optInt("index");
            pista.idioma = texto(p, "language");
            pista.titulo = texto(p, "title");
            pista.codec = texto(p, "codec");
            pista.canales = p.optInt("channels");
            a.audios.add(pista);
        }
        return a;
    }

    private static String nombreExtra(String tipo) {
        switch (tipo) {
            case "trailer": return "Tráiler";
            case "featurette": return "Reportaje";
            case "behindthescenes": return "Detrás de las cámaras";
            case "deleted": return "Escena eliminada";
            case "interview": return "Entrevista";
            case "scene": return "Escena";
            case "short": return "Cortometraje";
            default: return "Extra";
        }
    }

    private static String texto(JSONObject o, String clave) {
        return o.isNull(clave) ? "" : o.optString(clave);
    }
}
