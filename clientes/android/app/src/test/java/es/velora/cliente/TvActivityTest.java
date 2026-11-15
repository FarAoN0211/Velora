package es.velora.cliente;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;
import static org.robolectric.Shadows.shadowOf;

import android.app.AlertDialog;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageInstaller;
import android.content.pm.PackageManager;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Rect;
import android.os.Looper;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.webkit.CookieManager;
import android.widget.EditText;
import android.widget.GridView;
import android.widget.TextView;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.function.BooleanSupplier;
import org.json.JSONObject;
import org.junit.After;
import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.shadows.ShadowAlertDialog;
import org.robolectric.shadows.ShadowLooper;
import org.robolectric.shadows.ShadowToast;
import org.robolectric.util.ReflectionHelpers;

/** Recorre las pantallas reales con el mando simulado y un servidor local con las mismas respuestas que la API. No sustituye a la prueba en la TV Box. */
@RunWith(RobolectricTestRunner.class)
@Config(sdk = 34, qualifiers = "w960dp-h540dp-land-television-mdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class TvActivityTest {
    private static final String LIB = "b1";
    private HttpServer servidor;
    private final List<String> pedidas = Collections.synchronizedList(new ArrayList<>());
    private volatile boolean conSesion = true;
    private volatile int bibliotecaTotal = 150;
    private ActivityController<TvActivity> control;
    private TvActivity app;

    @Before
    public void arrancarServidor() throws Exception {
        servidor = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        servidor.createContext("/", this::responder);
        servidor.start();
        Servidor.guardar(contexto(), "http://127.0.0.1:" + servidor.getAddress().getPort());
        shadowOf(contexto().getPackageManager()).setSystemFeature(PackageManager.FEATURE_LEANBACK, true);
    }

    @After
    public void parar() {
        if (control != null) control.pause().stop().destroy();
        servidor.stop(0);
    }

    private static Context contexto() {
        return RuntimeEnvironment.getApplication();
    }

    private void responder(HttpExchange h) throws IOException {
        String ruta = h.getRequestURI().getPath();
        String q = h.getRequestURI().getRawQuery();
        pedidas.add(h.getRequestMethod() + " " + ruta + (q == null ? "" : "?" + q));
        try {
            if (ruta.contains("/images/")) {
                enviar(h, 200, "image/png", png());
            } else if (ruta.equals("/ota/velora.json")) {
                enviar(h, 404, "text/plain", new byte[0]);
            } else if (ruta.equals("/api/v1/auth/profiles")) {
                json(h, "{\"profiles\":[{\"id\":\"p1\",\"display_name\":\"Sala\",\"has_pin\":false}]}");
            } else if (ruta.equals("/api/v1/auth/login")) {
                String cuerpo = new String(h.getRequestBody().readAllBytes(), StandardCharsets.UTF_8);
                if (!"secreta".equals(new JSONObject(cuerpo).optString("password"))) {
                    enviar(h, 401, "application/json", "{\"error\":{\"code\":\"x\",\"message\":\"no\"}}".getBytes(StandardCharsets.UTF_8));
                    return;
                }
                conSesion = true;
                h.getResponseHeaders().add("Set-Cookie", "velora_session=abc; Path=/; HttpOnly");
                h.getResponseHeaders().add("Set-Cookie", "velora_csrf=tok; Path=/");
                json(h, "{\"user\":{\"id\":\"p1\"}}");
            } else if (ruta.equals("/api/v1/system/info")) {
                json(h, "{\"setup_required\":false}");
            } else if (!conSesion) {
                enviar(h, 401, "application/json", "{\"error\":{\"code\":\"x\",\"message\":\"no\"}}".getBytes(StandardCharsets.UTF_8));
            } else if (ruta.equals("/api/v1/me")) {
                json(h, "{\"user\":{\"display_name\":\"Sala\",\"is_admin\":true}}");
            } else if (ruta.equals("/api/v1/libraries")) {
                json(h, "{\"libraries\":[{\"id\":\"" + LIB + "\",\"name\":\"Películas\",\"kind\":\"movies\",\"in_menu\":true},{\"id\":\"b2\",\"name\":\"Series\",\"kind\":\"tv\",\"in_menu\":true}]}");
            } else if (ruta.equals("/api/v1/home")) {
                json(h, "{\"featured\":[" + carta("d1", "Destacada", 0) + "],\"continue\":[" + carta("c1", "A medias", 600000) + "],\"rows\":[{\"library\":{\"name\":\"Películas\"},\"items\":" + cartas(0, 20) + "},{\"library\":{\"name\":\"Series\"},\"items\":" + cartas(100, 5) + "}]}");
            } else if (ruta.equals("/api/v1/genres")) {
                json(h, "{\"genres\":[{\"name\":\"Drama\",\"count\":3}]}");
            } else if (ruta.equals("/api/v1/items")) {
                if (q.contains("q=")) {
                    json(h, "{\"items\":" + (q.contains("nada") ? "[]" : cartas(0, 3)) + "}");
                    return;
                }
                int desde = Integer.parseInt(q.replaceAll(".*offset=(\\d+).*", "$1"));
                json(h, "{\"items\":" + cartas(desde, Math.max(0, Math.min(60, bibliotecaTotal - desde))) + "}");
            } else if (ruta.endsWith("/subtitles")) {
                json(h, "{\"subtitles\":[{\"id\":\"i3\",\"label\":\"Español · Descriptivos (SDH/CC)\",\"supported\":true}]}");
            } else if (ruta.startsWith("/api/v1/items/")) {
                json(h, "{\"item\":" + ficha(ruta.substring("/api/v1/items/".length())) + "}");
            } else if (h.getRequestMethod().equals("POST") || h.getRequestMethod().equals("DELETE")) {
                enviar(h, 204, "text/plain", null);
            } else {
                enviar(h, 404, "text/plain", new byte[0]);
            }
        } catch (Exception e) {
            enviar(h, 500, "text/plain", String.valueOf(e).getBytes(StandardCharsets.UTF_8));
        }
    }

    private static void json(HttpExchange h, String t) throws IOException {
        enviar(h, 200, "application/json", t.getBytes(StandardCharsets.UTF_8));
    }

    private static void enviar(HttpExchange h, int estado, String tipo, byte[] d) throws IOException {
        h.getResponseHeaders().set("Content-Type", tipo);
        h.sendResponseHeaders(estado, d == null ? -1 : d.length == 0 ? -1 : d.length);
        if (d != null && d.length > 0) h.getResponseBody().write(d);
        h.close();
    }

    private static byte[] png() {
        Bitmap b = Bitmap.createBitmap(40, 60, Bitmap.Config.ARGB_8888);
        b.eraseColor(Color.rgb(70, 100, 150));
        ByteArrayOutputStream o = new ByteArrayOutputStream();
        b.compress(Bitmap.CompressFormat.PNG, 100, o);
        return o.toByteArray();
    }

    private static String carta(String id, String titulo, long posicion) {
        return "{\"id\":\"" + id + "\",\"library_id\":\"" + LIB + "\",\"title\":\"" + titulo + "\",\"year\":2021,\"has_poster\":true,\"has_backdrop\":true,\"position_ms\":" + posicion
                + ",\"runtime_ms\":6000000,\"played\":false,\"overview\":\"Una sinopsis con varias frases para ver cómo se recorta el texto en la cabecera del inicio de la aplicación, que debe ocupar poco y dejar ver el carrusel de debajo.\"}";
    }

    private static String cartas(int desde, int n) {
        StringBuilder b = new StringBuilder("[");
        for (int i = desde; i < desde + n; i++) b.append(i == desde ? "" : ",").append(carta("i" + i, "Película " + i, 0));
        return b.append("]").toString();
    }

    private static String ficha(String id) {
        String c = carta(id, "Película con un título bastante largo para comprobar", 600000);
        return c.substring(0, c.length() - 1) + ",\"tagline\":\"Un lema\",\"official_rating\":\"12\",\"quality\":\"1080p\",\"genres\":[\"Drama\",\"Acción\"],\"files\":["
                + "{\"id\":\"f1\",\"missing\":false,\"version_label\":\"Montaje del director\",\"streams\":[{\"index\":1,\"kind\":\"audio\",\"language\":\"spa\",\"codec\":\"ac3\",\"channels\":6},{\"index\":2,\"kind\":\"audio\",\"language\":\"eng\",\"codec\":\"dts\",\"channels\":6}]},"
                + "{\"id\":\"f2\",\"missing\":false,\"version_label\":null,\"streams\":[{\"index\":0,\"kind\":\"video\",\"height\":720}]}],"
                + "\"extras\":[{\"id\":\"e1\",\"kind\":\"trailer\",\"title\":\"\"},{\"id\":\"e2\",\"kind\":\"interview\",\"title\":\"\"}]}";
    }

    @Test
    public void subtitulosDeFichaSeEnvianAlReproductor() throws Exception {
        abrir(); app.findViewById(android.R.id.content).findViewWithTag("continuar:c1").performClick();
        esperar(() -> texto("Subtítulos: desactivados") != null);
        texto("Subtítulos: desactivados").performClick();
        esperar(() -> ShadowAlertDialog.getLatestAlertDialog() != null);
        AlertDialog dialogo = ShadowAlertDialog.getLatestAlertDialog();
        assertEquals("Español · Descriptivos (SDH/CC)", dialogo.getListView().getAdapter().getItem(1));
        dialogo.getListView().performItemClick(null,1,1);
        texto("Continuar").performClick();
        Intent intent=shadowOf(app).getNextStartedActivity();
        JSONObject datos=new JSONObject(intent.getStringExtra(ReproductorActivity.DATOS));
        assertEquals("i3",datos.getString("sub")); assertEquals("f1",datos.getString("file"));
    }

    // ------- Auxiliares -------

    private void abrir() {
        control = Robolectric.buildActivity(TvActivity.class).setup();
        app = control.get();
        modoMando();
        esperar(() -> texto("Destacada") != null || texto("¿Quién está viendo?") != null);
        componer();
    }

    /** Como tras la primera pulsación en la TV Box. */
    private void modoMando() {
        Object ventana = ReflectionHelpers.callInstanceMethod(raiz(), "getViewRootImpl");
        ReflectionHelpers.callInstanceMethod(ventana, "ensureTouchMode", ReflectionHelpers.ClassParameter.from(boolean.class, false));
    }

    private void esperar(BooleanSupplier hasta) {
        long fin = System.currentTimeMillis() + 8000;
        while (System.currentTimeMillis() < fin) {
            shadowOf(Looper.getMainLooper()).idle();
            if (hasta.getAsBoolean()) return;
            try {
                Thread.sleep(10);
            } catch (InterruptedException e) {
                throw new IllegalStateException(e);
            }
        }
        throw new AssertionError("No ha llegado a tiempo. Peticiones: " + pedidas);
    }

    private void componer() {
        shadowOf(Looper.getMainLooper()).idle();
        View d = app.getWindow().getDecorView();
        int w = contexto().getResources().getDisplayMetrics().widthPixels;
        int h = contexto().getResources().getDisplayMetrics().heightPixels;
        d.measure(View.MeasureSpec.makeMeasureSpec(w, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(h, View.MeasureSpec.EXACTLY));
        d.layout(0, 0, w, h);
        shadowOf(Looper.getMainLooper()).idle();
    }

    private View raiz() {
        return app.getWindow().getDecorView();
    }

    private TextView texto(String t) {
        List<TextView> r = new ArrayList<>();
        recoger(raiz(), TextView.class, r);
        for (TextView v : r) if (t.contentEquals(v.getText())) return v;
        return null;
    }

    private static <T extends View> void recoger(View v, Class<T> tipo, List<T> r) {
        if (tipo.isInstance(v)) r.add(tipo.cast(v));
        if (v instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) v).getChildCount(); i++) recoger(((ViewGroup) v).getChildAt(i), tipo, r);
    }

    private void tecla(int codigo) {
        app.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, codigo));
        app.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_UP, codigo));
        componer();
    }

    private String foco() {
        View f = raiz().findFocus();
        if (f instanceof TextView) return ((TextView) f).getText().toString();
        return f == null ? "(nada)" : String.valueOf(f.getTag());
    }

    private static Rect marco(View v) {
        int[] p = new int[2];
        v.getLocationInWindow(p);
        return new Rect(p[0], p[1], p[0] + v.getWidth(), p[1] + v.getHeight());
    }

    private void entrarEnSeccion(String nombre) {
        texto(nombre).performClick();
        componer();
    }

    private void foto(String nombre) throws IOException {
        File dir = new File(System.getProperty("velora.fotos", ""));
        if (!dir.isDirectory()) return;
        componer();
        View d = raiz();
        Bitmap b = Bitmap.createBitmap(d.getWidth(), d.getHeight(), Bitmap.Config.ARGB_8888);
        d.draw(new Canvas(b));
        try (FileOutputStream o = new FileOutputStream(new File(dir, nombre + ".png"))) {
            b.compress(Bitmap.CompressFormat.PNG, 100, o);
        }
    }

    private void comprobarInicioCompone() {
        int alto = raiz().getHeight();
        View cabecera = (View) texto("Destacada").getParent().getParent();
        assertTrue("cabecera de " + cabecera.getHeight() + " en " + alto, cabecera.getHeight() <= alto * 0.40);
        View carrusel = texto("Continuar viendo");
        assertTrue("el primer carrusel asoma", marco(carrusel).bottom < alto);
        Rect primera = marco(app.findViewById(android.R.id.content).findViewWithTag("continuar:c1"));
        assertTrue("su carátula se ve entera: " + primera, primera.bottom <= alto);
    }

    // ------- Pruebas -------

    @Test
    public void elLateralVaALaIzquierdaConBuscarAntesDeLasBibliotecas() throws Exception {
        abrir();
        Rect inicio = marco(texto("Inicio"));
        Rect buscar = marco(texto("Buscar"));
        Rect peliculas = marco(texto("Películas"));
        assertTrue(buscar.top > inicio.top);
        assertTrue(peliculas.top > buscar.top);
        Rect destacada = marco(texto("Destacada"));
        assertTrue("el lateral queda a la izquierda", inicio.right <= destacada.left);
        assertTrue("Panel de control solo para administración", texto("Panel de control") != null);
        foto("inicio-960x540");
    }

    @Test
    public void elInicioComponeA960x540() {
        abrir();
        comprobarInicioCompone();
    }

    @Test
    @Config(qualifiers = "w1280dp-h720dp-land-television-mdpi")
    public void elInicioComponeA1280x720() throws Exception {
        abrir();
        comprobarInicioCompone();
        foto("inicio-1280x720");
    }

    @Test
    public void soloCaratulasYAccionesRecibenElFoco() {
        abrir();
        ArrayList<View> posibles = new ArrayList<>();
        app.findViewById(android.R.id.content).addFocusables(posibles, View.FOCUS_DOWN);
        assertFalse(posibles.isEmpty());
        for (View v : posibles) {
            boolean carta = v instanceof VistasTv.Carta;
            boolean accion = v instanceof TextView && ((TextView) v).isClickable();
            assertTrue("no debería recibir el foco: " + v, carta || accion);
        }
        assertFalse(texto("Continuar viendo").isFocusable());
        assertFalse(texto("Película 0").isFocusable());
    }

    @Test
    public void elMandoRecorreCaratulasSinSaltarDeFilaNiAlLateral() {
        abrir();
        assertEquals("Reproducir", foco());
        tecla(KeyEvent.KEYCODE_DPAD_RIGHT);
        assertEquals("Más información", foco());
        tecla(KeyEvent.KEYCODE_DPAD_RIGHT);
        assertEquals("el final de la fila no salta", "Más información", foco());
        tecla(KeyEvent.KEYCODE_DPAD_DOWN);
        assertEquals("continuar:c1", foco());
        tecla(KeyEvent.KEYCODE_DPAD_RIGHT);
        assertEquals("una fila de una sola carátula no salta a otra", "continuar:c1", foco());
        tecla(KeyEvent.KEYCODE_DPAD_DOWN);
        assertTrue(foco(), foco().startsWith("fila0:"));
        String enFila = foco();
        tecla(KeyEvent.KEYCODE_DPAD_RIGHT);
        assertTrue(foco(), foco().startsWith("fila0:") && !foco().equals(enFila));
        tecla(KeyEvent.KEYCODE_DPAD_LEFT);
        assertEquals(enFila, foco());
        tecla(KeyEvent.KEYCODE_DPAD_DOWN);
        assertTrue(foco(), foco().startsWith("fila1:"));
        tecla(KeyEvent.KEYCODE_DPAD_DOWN);
        assertTrue("abajo del todo no va al lateral: " + foco(), foco().startsWith("fila1:"));
        String ultima = foco();
        for (int i = 0; i < 3 && !foco().equals("Inicio"); i++) tecla(KeyEvent.KEYCODE_DPAD_LEFT);
        assertEquals("a la izquierda se entra en el lateral, en la sección abierta", "Inicio", foco());
        tecla(KeyEvent.KEYCODE_DPAD_DOWN);
        assertEquals("Buscar", foco());
        tecla(KeyEvent.KEYCODE_DPAD_LEFT);
        assertEquals("Buscar", foco());
        tecla(KeyEvent.KEYCODE_DPAD_RIGHT);
        assertTrue("vuelve a la última carátula: " + foco() + " tras " + ultima, foco().startsWith("fila1:"));
    }

    @Test
    public void laSeccionAbiertaCambiaDeColorYElFocoTambien() {
        abrir();
        TextView inicio = texto("Inicio");
        assertTrue(inicio.isActivated());
        assertEquals(VistasTv.ACENTO, inicio.getCurrentTextColor());
        assertEquals(VistasTv.TEXTO, texto("Buscar").getCurrentTextColor());
        inicio.requestFocus();
        componer();
        assertEquals(VistasTv.SOBRE_ACENTO, inicio.getCurrentTextColor());
    }

    @Test
    public void laFichaLoEnseñaTodoALaVez() throws Exception {
        abrir();
        app.findViewById(android.R.id.content).findViewWithTag("continuar:c1").performClick();
        esperar(() -> texto("Continuar") != null && pedidas.contains("GET /api/v1/items/c1"));
        componer();
        int ancho = raiz().getWidth();
        int alto = raiz().getHeight();
        TextView titulo = texto("Película con un título bastante largo para comprobar");
        assertNotNull(titulo);
        Rect marcoTitulo = marco(titulo);
        Rect play = marco(texto("Continuar"));
        assertTrue("título por encima de las acciones", marcoTitulo.bottom <= play.top);
        assertTrue("acciones dentro de la pantalla: " + play, play.bottom <= alto && play.right <= ancho);
        assertTrue(marco(texto("2021 · 1 h 40 min · 12 · 1080p · Drama, Acción")).bottom <= play.top);
        assertEquals("Continuar", foco());
        assertNotNull(texto("Audio"));
        assertTrue("opciones después de las acciones", marco(texto("Versión: Montaje del director")).top > play.top);
        assertNotNull(texto("Subtítulos: desactivados"));
        foto("ficha-960x540");
        assertTrue(texto("Inicio").isActivated());
    }

    @Test
    @Config(qualifiers = "w1280dp-h720dp-land-television-mdpi")
    public void laFichaComponeA1280x720() {
        abrir();
        app.findViewById(android.R.id.content).findViewWithTag("continuar:c1").performClick();
        esperar(() -> texto("Continuar") != null);
        componer();
        Rect play = marco(texto("Continuar"));
        assertTrue(play.bottom <= raiz().getHeight());
    }

    @Test
    public void reproducirAbreElReproductorPropioYAtrasNoVuelveAlVideo() throws Exception {
        abrir();
        tecla(KeyEvent.KEYCODE_DPAD_DOWN);
        assertEquals("continuar:c1", foco());
        tecla(KeyEvent.KEYCODE_DPAD_CENTER);
        esperar(() -> texto("Continuar") != null);
        componer();
        assertEquals("Continuar", foco());
        tecla(KeyEvent.KEYCODE_DPAD_CENTER);
        Intent abierta = shadowOf(app).getNextStartedActivity();
        assertEquals(ReproductorActivity.class.getName(), abierta.getComponent().getClassName());
        JSONObject datos = new JSONObject(abierta.getStringExtra(ReproductorActivity.DATOS));
        assertEquals("c1", datos.getString("item"));
        assertEquals("f1", datos.getString("file"));
        assertFalse(datos.getBoolean("inicio"));

        // Sale del vídeo: se vuelve a pedir la ficha para enseñar el progreso, y Atrás lleva al inicio, no al vídeo.
        pedidas.clear();
        shadowOf(app).receiveResult(abierta, android.app.Activity.RESULT_OK, new Intent());
        esperar(() -> pedidas.contains("GET /api/v1/items/c1") && texto("Continuar") != null);
        assertNull(shadowOf(app).getNextStartedActivity());
        app.onBackPressed();
        esperar(() -> texto("Destacada") != null);
        componer();
        assertNull("Atrás no abre ningún vídeo", shadowOf(app).getNextStartedActivity());
        assertEquals("y el foco vuelve a la carátula", "continuar:c1", foco());
    }

    @Test
    public void siElReproductorPropioFallaSeAbreElAlternativo() {
        abrir();
        texto("Reproducir").performClick();
        Intent abierta = shadowOf(app).getNextStartedActivity();
        Intent fallo = new Intent().putExtra(ReproductorActivity.ITEM, "d1").putExtra(ReproductorActivity.ARCHIVO, "f1");
        shadowOf(app).receiveResult(abierta, ReproductorActivity.RESULTADO_FALLO, fallo);
        Intent web = shadowOf(app).getNextStartedActivity();
        assertEquals(MainActivity.class.getName(), web.getComponent().getClassName());
        assertEquals("/ver/d1?web=1&file=f1", web.getStringExtra(MainActivity.RUTA));
    }

    @Test
    public void atrasEnElInicioOfreceSalirYLaSegundaSale() {
        abrir();
        app.onBackPressed();
        assertEquals("Pulsa otra vez para salir", ShadowToast.getTextOfLatestToast());
        assertFalse(app.isFinishing());
        app.onBackPressed();
        assertTrue(app.isFinishing());
    }

    @Test
    public void laBibliotecaNoTieneBuscadorYCargaPaginas() throws Exception {
        abrir();
        entrarEnSeccion("Películas");
        esperar(() -> texto("Película 0") != null);
        componer();
        List<EditText> campos = new ArrayList<>();
        recoger(raiz(), EditText.class, campos);
        assertTrue("sin buscador dentro de la biblioteca", campos.isEmpty());
        foto("biblioteca-960x540");
        List<GridView> rejillas = new ArrayList<>();
        recoger(raiz(), GridView.class, rejillas);
        GridView g = rejillas.get(0);
        assertEquals(g, raiz().findFocus());
        int columnas = g.getNumColumns();
        assertTrue("caben sin recortes", columnas * VistasTv.ANCHO_CARATULA + (columnas - 1) * VistasTv.HUECO + 40 <= raiz().getWidth() - VistasTv.ANCHO_LATERAL);
        assertEquals(6, columnas);
        // Las flechas navegan por la rejilla y a la izquierda se sale al lateral.
        tecla(KeyEvent.KEYCODE_DPAD_RIGHT);
        assertEquals(1, g.getSelectedItemPosition());
        tecla(KeyEvent.KEYCODE_DPAD_DOWN);
        assertEquals(1 + g.getNumColumns(), g.getSelectedItemPosition());
        tecla(KeyEvent.KEYCODE_DPAD_LEFT);
        assertEquals(g.getNumColumns(), g.getSelectedItemPosition());
        tecla(KeyEvent.KEYCODE_DPAD_LEFT);
        assertEquals("Películas", foco());
        tecla(KeyEvent.KEYCODE_DPAD_RIGHT);
        assertEquals(g, raiz().findFocus());
        // Arriba del todo, los filtros.
        tecla(KeyEvent.KEYCODE_DPAD_UP);
        tecla(KeyEvent.KEYCODE_DPAD_UP);
        assertTrue(foco(), texto("Título") == raiz().findFocus() || foco().equals("A → Z") || foco().equals("Género") || foco().equals("Todas"));
        tecla(KeyEvent.KEYCODE_DPAD_DOWN);
        assertEquals(g, raiz().findFocus());
        // Al acercarse al final de lo cargado se pide lo siguiente, hasta que no queda más.
        while (g.getAdapter().getCount() < bibliotecaTotal) {
            final int antes = g.getAdapter().getCount();
            g.setSelection(antes - 1);
            esperar(() -> {
                componer();
                return g.getAdapter().getCount() > antes;
            });
        }
        assertEquals(bibliotecaTotal, g.getAdapter().getCount());
        assertTrue(pedidas.toString(), pedidas.stream().anyMatch(p -> p.contains("offset=60")));
        assertTrue(pedidas.toString(), pedidas.stream().anyMatch(p -> p.contains("offset=120")));
    }

    @Test
    public void atrasDesdeUnaFichaDeLaBibliotecaVuelveALaMismaCaratula() {
        abrir();
        entrarEnSeccion("Películas");
        esperar(() -> texto("Película 0") != null);
        componer();
        List<GridView> rejillas = new ArrayList<>();
        recoger(raiz(), GridView.class, rejillas);
        GridView g = rejillas.get(0);
        tecla(KeyEvent.KEYCODE_DPAD_RIGHT);
        tecla(KeyEvent.KEYCODE_DPAD_RIGHT);
        int pos = g.getSelectedItemPosition();
        g.performItemClick(g.getSelectedView(), pos, pos);
        esperar(() -> texto("Continuar") != null);
        assertTrue("la sección sigue marcada en la ficha", texto("Películas").isActivated());
        pedidas.clear();
        app.onBackPressed();
        esperar(() -> texto("Título") != null);
        componer();
        assertFalse("no vuelve a pedir la lista", pedidas.stream().anyMatch(p -> p.startsWith("GET /api/v1/items?")));
        recoger(raiz(), GridView.class, rejillas = new ArrayList<>());
        assertEquals(pos, rejillas.get(0).getSelectedItemPosition());
        app.onBackPressed();
        esperar(() -> texto("Destacada") != null);
        assertTrue("desde una sección, Atrás va al inicio", texto("Inicio").isActivated());
    }

    @Test
    public void buscarConsultaYAbreResultadosConElMando() throws Exception {
        abrir();
        entrarEnSeccion("Buscar");
        List<EditText> campos = new ArrayList<>();
        recoger(raiz(), EditText.class, campos);
        assertEquals(1, campos.size());
        EditText campo = campos.get(0);
        assertEquals(campo, raiz().findFocus());
        campo.setText("casa");
        ShadowLooper.idleMainLooper(400, java.util.concurrent.TimeUnit.MILLISECONDS);
        esperar(() -> texto("Película 0") != null);
        componer();
        assertTrue(pedidas.toString(), pedidas.contains("GET /api/v1/items?limit=60&q=casa"));
        foto("buscar-960x540");
        tecla(KeyEvent.KEYCODE_DPAD_DOWN);
        List<GridView> rejillas = new ArrayList<>();
        recoger(raiz(), GridView.class, rejillas);
        assertEquals("abajo baja a los resultados", rejillas.get(0), raiz().findFocus());
        GridView g = rejillas.get(0);
        g.performItemClick(g.getSelectedView(), g.getSelectedItemPosition(), g.getSelectedItemPosition());
        esperar(() -> texto("Continuar") != null);
        pedidas.clear();
        app.onBackPressed();
        componer();
        esperar(() -> texto("Película 0") != null);
        assertFalse("conserva los resultados", pedidas.stream().anyMatch(p -> p.contains("q=casa")));
    }

    @Test
    public void buscarSinResultadosLoDice() {
        abrir();
        entrarEnSeccion("Buscar");
        List<EditText> campos = new ArrayList<>();
        recoger(raiz(), EditText.class, campos);
        campos.get(0).setText("nada");
        ShadowLooper.idleMainLooper(400, java.util.concurrent.TimeUnit.MILLISECONDS);
        esperar(() -> texto("No hay resultados para «nada».") != null);
    }

    @Test
    public void sinSesionSeEntraSinWebYLaSesionQuedaParaTodaLaApp() throws Exception {
        CookieManager.getInstance().removeAllCookies(null);
        conSesion = false;
        abrir();
        assertEquals("sin lateral antes de entrar", View.GONE, ((View) texto("Inicio") == null ? View.GONE : View.VISIBLE));
        foto("entrada-960x540");
        assertEquals("Sala", foco());
        raiz().findFocus().performClick();
        componer();
        List<EditText> campos = new ArrayList<>();
        recoger(raiz(), EditText.class, campos);
        assertEquals("con un perfil basta la contraseña", 1, campos.size());
        campos.get(0).setText("mal");
        texto("Entrar").performClick();
        esperar(() -> texto("Usuario o contraseña incorrectos.") != null);
        assertEquals("", campos.get(0).getText().toString());
        campos.get(0).setText("secreta");
        texto("Entrar").performClick();
        esperar(() -> texto("Destacada") != null);
        assertTrue(CookieManager.getInstance().getCookie(Servidor.leer(contexto())).contains("velora_session=abc"));
    }

    @Test
    public void sinServidorOfreceReintentarYCambiarlo() {
        servidor.stop(0);
        control = Robolectric.buildActivity(TvActivity.class).setup();
        app = control.get();
        modoMando();
        esperar(() -> texto("Reintentar") != null);
        componer();
        assertEquals("Reintentar", foco());
        assertNotNull(texto("Cambiar servidor"));
    }

    @Test
    public void laTeclaMenuYElOkMantenidoAbrenElMenuDeLaCaratula() {
        abrir();
        tecla(KeyEvent.KEYCODE_DPAD_DOWN);
        tecla(KeyEvent.KEYCODE_MENU);
        AlertDialog menu = ShadowAlertDialog.getLatestAlertDialog();
        assertNotNull(menu);
        List<String> opciones = new ArrayList<>();
        for (int i = 0; i < menu.getListView().getCount(); i++) opciones.add(String.valueOf(menu.getListView().getItemAtPosition(i)));
        assertEquals("[Continuar, Desde el principio, Marcar como vista, Quitar de «Continuar viendo», Ir a la ficha]", opciones.toString());
        menu.dismiss();
        ShadowAlertDialog.reset();
        assertTrue(raiz().findFocus().performLongClick());
        assertNotNull(ShadowAlertDialog.getLatestAlertDialog());
    }

    private static Intent respuesta(int intento, int sesion, int estado, String mensaje) {
        return new Intent().putExtra(DiagnosticoOta.EXTRA_INTENTO, intento).putExtra(PackageInstaller.EXTRA_SESSION_ID, sesion)
                .putExtra(PackageInstaller.EXTRA_STATUS, estado).putExtra(PackageInstaller.EXTRA_STATUS_MESSAGE, mensaje);
    }

    private static String mensajeUltimoDialogo() {
        AlertDialog d = ShadowAlertDialog.getLatestAlertDialog();
        assertNotNull(d);
        return shadowOf(d).getMessage().toString();
    }

    @Test
    public void elFalloDeInstalacionPasaPorElCatchRealYLaRespuestaTardiaNoLoPisa() {
        abrir();
        Actualizador actualizador = ReflectionHelpers.getField(app, "actualizador");
        // Sin el archivo falla la apertura: el mismo catch que un fallo de copia o de cierre, con la sesión ya creada.
        boolean entregada = ReflectionHelpers.callInstanceMethod(actualizador, "entregarAlSistema",
                ReflectionHelpers.ClassParameter.from(File.class, new File(contexto().getCacheDir(), "no-existe.apk")));
        assertFalse(entregada);
        actualizador.continuar();
        String primero = mensajeUltimoDialogo();
        assertTrue(primero, primero.contains("Fallo en «abrir la sesión y el archivo»: FileNotFoundException"));
        assertTrue(primero, primero.contains("La app ha abandonado la sesión"));
        assertTrue(primero, primero.indexOf("FileNotFoundException") < primero.indexOf("ha abandonado"));
        assertTrue(primero, primero.contains("intento 1"));
        ShadowAlertDialog.getLatestAlertDialog().dismiss();
        ShadowAlertDialog.reset();

        // Llega después de enseñarlo, como tras abandonar una sesión: se vuelve a enseñar, con la excepción original delante.
        new InstallResult().onReceive(contexto(), respuesta(1, 5, PackageInstaller.STATUS_FAILURE_ABORTED, "Session was abandoned"));
        actualizador.continuar();
        String segundo = mensajeUltimoDialogo();
        assertTrue(segundo, segundo.indexOf("FileNotFoundException") >= 0);
        assertTrue(segundo, segundo.indexOf("FileNotFoundException") < segundo.indexOf("cancelada o abandonada (3): Session was abandoned"));
        assertTrue(segundo, segundo.contains("sesión 5"));
        ShadowAlertDialog.getLatestAlertDialog().dismiss();
        ShadowAlertDialog.reset();

        actualizador.continuar();
        assertNull("sin novedades no se repite", ShadowAlertDialog.getLatestAlertDialog());
    }

    @Test
    public void unFalloSeguidoDeSuRespuestaSeVeEntreAlAbrirLaApp() {
        DiagnosticoOta.anotarFallo(contexto(), 2, 8, "cerrar tras confirmar la sesión", new IOException("write failed: EBADF"));
        new InstallResult().onReceive(contexto(), respuesta(2, 8, PackageInstaller.STATUS_FAILURE_ABORTED, "Session was abandoned"));
        abrir();
        String m = mensajeUltimoDialogo();
        assertTrue(m, m.indexOf("EBADF") >= 0);
        assertTrue(m, m.indexOf("EBADF") < m.indexOf("Session was abandoned"));
    }

    @Test
    public void pedirConfirmacionSeAnotaPeroNoSeEnseñaComoFallo() {
        new InstallResult().onReceive(contexto(), respuesta(3, 9, PackageInstaller.STATUS_PENDING_USER_ACTION, null));
        abrir();
        assertNull(ShadowAlertDialog.getLatestAlertDialog());
    }

    @Test
    public void unaInstalacionCorrectaNoDejaDiagnostico() {
        new InstallResult().onReceive(contexto(), new Intent().putExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_SUCCESS));
        abrir();
        assertNull(ShadowAlertDialog.getLatestAlertDialog());
    }

    @Test
    public void marcarVistaDesdeLaFichaLlamaALaApi() {
        abrir();
        app.findViewById(android.R.id.content).findViewWithTag("continuar:c1").performClick();
        esperar(() -> texto("Marcar como vista") != null);
        texto("Marcar como vista").performClick();
        esperar(() -> pedidas.contains("POST /api/v1/me/items/c1/played"));
    }

    @Test
    public void dosCierresAlArrancarDevuelvenLaWebYUnaVersionNuevaLoReintenta() {
        assertTrue(GuardianTv.nativaPermitida(contexto()));
        GuardianTv.arranque(contexto());
        assertTrue(GuardianTv.nativaPermitida(contexto()));
        GuardianTv.arranque(contexto());
        assertFalse(GuardianTv.nativaPermitida(contexto()));
        GuardianTv.estable(contexto());
        assertTrue(GuardianTv.nativaPermitida(contexto()));
    }

    @Test
    public void enTvLaAppArrancaSiempreEnLaWeb() {
        MainActivity m = Robolectric.buildActivity(MainActivity.class).setup().get();
        assertFalse(m.isFinishing());
        assertNull(shadowOf(m).getNextStartedActivity());
    }

    @Test
    public void conDosCierresLaAppSigueEnLaWebYBuscaActualizaciones() throws Exception {
        GuardianTv.arranque(contexto());
        GuardianTv.arranque(contexto());
        MainActivity m = Robolectric.buildActivity(MainActivity.class).setup().get();
        assertFalse(m.isFinishing());
        assertNull(shadowOf(m).getNextStartedActivity());
        esperar(() -> pedidas.contains("GET /ota/velora.json"));
    }

    @Test
    public void elCierreLimpioNoCuentaComoFallo() {
        abrir();
        control.pause().stop();
        assertTrue(GuardianTv.nativaPermitida(contexto()));
        GuardianTv.arranque(contexto());
        assertTrue("un cierre aislado no aparta la pantalla nativa", GuardianTv.nativaPermitida(contexto()));
        control.destroy();
        control = null;
    }

    @Test
    public void laPantallaNativaBuscaActualizacionesAunqueNoHayaSesion() {
        conSesion = false;
        abrir();
        esperar(() -> pedidas.contains("GET /ota/velora.json"));
    }
}
