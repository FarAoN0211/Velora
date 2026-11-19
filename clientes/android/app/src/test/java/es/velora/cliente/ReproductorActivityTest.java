package es.velora.cliente;

import android.view.KeyEvent;
import android.view.View;
import android.widget.TextView;
import androidx.media3.exoplayer.ExoPlayer;
import org.json.JSONObject;
import org.junit.After;
import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.util.ReflectionHelpers;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.util.List;
import java.util.concurrent.ExecutorService;
import static org.junit.Assert.*;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 34, qualifiers = "w960dp-h540dp-land-television-mdpi")
public class ReproductorActivityTest {
    private ReproductorActivity app;
    private ActivityController<ReproductorActivity> control;

    @Before public void preparar() throws Exception {
        // Probar la interfaz aislada, sin preparar contenido ni iniciar peticiones de red.
        control = Robolectric.buildActivity(ReproductorActivity.class);
        app = control.get();
        llamar("construirInterfaz");
        poner("player", new ExoPlayer.Builder(app).build());
        ((List<JSONObject>) campo("capitulos")).add(new JSONObject()
                .put("kind", "intro").put("start", 10).put("end", 30));
        vista("controles").setVisibility(View.GONE);
        vista("cabecera").setVisibility(View.GONE);
        control.visible();
        // Como en la TV tras la primera pulsación: ventana real y foco de mando, no táctil.
        Object ventana = ReflectionHelpers.callInstanceMethod(app.getWindow().getDecorView(), "getViewRootImpl");
        ReflectionHelpers.callInstanceMethod(ventana, "ensureTouchMode",
                ReflectionHelpers.ClassParameter.from(boolean.class, false));
    }

    @After public void cerrar() throws Exception {
        ((ExoPlayer) campo("player")).release();
        ((ExecutorService) campo("fondo")).shutdownNow();
        ((ExecutorService) campo("auxiliar")).shutdownNow();
        ((android.os.Handler) campo("hilo")).removeCallbacksAndMessages(null);
    }

    @Test public void tocarVideoMuestraBotonesSinCambiarReproduccion() throws Exception {
        ExoPlayer player = (ExoPlayer) campo("player");
        player.setPlayWhenReady(true);
        llamar("mostrarControlesTactiles");
        assertTrue(player.getPlayWhenReady());
        assertEquals(View.VISIBLE, vista("controles").getVisibility());
        assertEquals(View.VISIBLE, vista("botones").getVisibility());
        assertEquals(View.VISIBLE, vista("cabecera").getVisibility());
    }

    @Test public void tocarVideoDuranteIntroNoMuestraControles() throws Exception {
        poner("especial", new JSONObject().put("kind", "intro"));
        llamar("mostrarControlesTactiles");
        assertEquals(View.GONE, vista("controles").getVisibility());
        assertEquals(View.GONE, vista("botones").getVisibility());
    }

    @Test public void conElPerfilBloqueadoVuelveALaWebParaPedirElPin() throws Exception {
        assertNotEquals(ReproductorActivity.RESULTADO_FALLO, ReproductorActivity.RESULTADO_BLOQUEADO);
        llamar("salirBloqueado");
        assertTrue(app.isFinishing());
        assertEquals(ReproductorActivity.RESULTADO_BLOQUEADO, org.robolectric.Shadows.shadowOf(app).getResultCode());
        assertTrue((Boolean) campo("terminado"));
        // Una segunda llamada (latido y error del vídeo a la vez) no cambia nada.
        llamar("salirBloqueado");
        assertEquals(ReproductorActivity.RESULTADO_BLOQUEADO, org.robolectric.Shadows.shadowOf(app).getResultCode());
    }

    @Test public void audioElegidoSeSeleccionaEnElArchivoDirectoSinReempaquetar() throws Exception {
        androidx.media3.common.TrackGroup ingles = new androidx.media3.common.TrackGroup("en", new androidx.media3.common.Format.Builder().setSampleMimeType("audio/mp4a-latm").setLanguage("en").build());
        androidx.media3.common.TrackGroup espanol = new androidx.media3.common.TrackGroup("es", new androidx.media3.common.Format.Builder().setSampleMimeType("audio/mp4a-latm").setLanguage("es").build());
        androidx.media3.common.Tracks pistas = new androidx.media3.common.Tracks(com.google.common.collect.ImmutableList.of(
                new androidx.media3.common.Tracks.Group(ingles, false, new int[] {androidx.media3.common.C.FORMAT_HANDLED}, new boolean[] {true}),
                new androidx.media3.common.Tracks.Group(espanol, false, new int[] {androidx.media3.common.C.FORMAT_HANDLED}, new boolean[] {false})));
        androidx.media3.common.TrackSelectionParameters base = new androidx.media3.common.TrackSelectionParameters.Builder(app).build();
        androidx.media3.common.TrackSelectionParameters[] aplicado = {null};
        ExoPlayer original = (ExoPlayer) campo("player");
        ExoPlayer doble = (ExoPlayer) java.lang.reflect.Proxy.newProxyInstance(ExoPlayer.class.getClassLoader(), new Class<?>[] {ExoPlayer.class}, (proxy, metodo, argumentos) -> {
            if ("getCurrentTracks".equals(metodo.getName())) return pistas;
            if ("getTrackSelectionParameters".equals(metodo.getName())) return base;
            if ("setTrackSelectionParameters".equals(metodo.getName())) {aplicado[0]=(androidx.media3.common.TrackSelectionParameters)argumentos[0];return null;}
            throw new AssertionError(metodo.getName());
        });
        try {
            poner("player", doble);
            List<JSONObject> audios=(List<JSONObject>)campo("audios");
            audios.add(new JSONObject().put("index",1));audios.add(new JSONObject().put("index",2));
            poner("audioPorAplicar",2);
            Method metodo=ReproductorActivity.class.getDeclaredMethod("aplicarAudioInicial",androidx.media3.common.Tracks.class);
            metodo.setAccessible(true);metodo.invoke(app,pistas);
            assertNotNull(aplicado[0]);
            assertTrue(aplicado[0].overrides.containsKey(espanol));
            assertEquals(-1,((Integer)campo("audioPorAplicar")).intValue());
        } finally {poner("player",original);}
    }

    @Test public void episodioAgrupaIdentidadDebajoDelLogoSinTituloInferior() throws Exception {
        JSONObject item = new JSONObject().put("kind", "episode").put("has_logo", true)
                .put("series_title", "Merlín").put("title", "La llamada del dragón")
                .put("season_number", 1).put("episode_number", 2);
        Method metodo = ReproductorActivity.class.getDeclaredMethod("aplicarCabecera", JSONObject.class);
        metodo.setAccessible(true);
        metodo.invoke(app, item);
        assertEquals("Temporada 1 · Episodio 2", ((TextView) vista("numeroEpisodio")).getText().toString());
        assertEquals("La llamada del dragón", ((TextView) vista("nombreEpisodio")).getText().toString());
        assertSame(campo("cabecera"), vista("logo").getParent());
        assertSame(campo("cabecera"), vista("numeroEpisodio").getParent());
        assertSame(campo("cabecera"), vista("nombreEpisodio").getParent());
        assertEquals(View.GONE, vista("titulo").getVisibility());
        assertEquals(View.GONE, vista("nombreSerie").getVisibility());
        metodo.invoke(app, item.put("has_logo", false));
        assertEquals(View.VISIBLE, vista("nombreSerie").getVisibility());
        assertEquals(View.GONE, vista("logo").getVisibility());
        metodo.invoke(app, item.put("title", ""));
        assertEquals(View.GONE, vista("nombreEpisodio").getVisibility());
        metodo.invoke(app, item.put("kind", "movie").put("title", "Película"));
        assertEquals(View.VISIBLE, vista("titulo").getVisibility());
        assertEquals(View.GONE, vista("numeroEpisodio").getVisibility());
        metodo.invoke(app, item.put("has_logo", true));
        assertEquals(View.GONE, vista("titulo").getVisibility());
    }

    @Test public void avisoRecuperaFocoTrasPrepararUnSaltoInclusoEnModoTactil() throws Exception {
        Object ventana = ReflectionHelpers.callInstanceMethod(app.getWindow().getDecorView(), "getViewRootImpl");
        ReflectionHelpers.callInstanceMethod(ventana, "ensureTouchMode", ReflectionHelpers.ClassParameter.from(boolean.class, true));
        actualizar(12000);
        assertTrue(vista("saltar").hasFocus());
        vista("raiz").requestFocus();
        actualizar(13000);
        assertTrue(vista("saltar").hasFocus());
        assertEquals(View.GONE, vista("controles").getVisibility());
        tecla(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_CENTER);
        tecla(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_DPAD_CENTER);
        assertEquals(30000L, ((Long) campo("objetivoMs")).longValue());
        assertEquals(View.GONE, vista("controles").getVisibility());
    }

    @Test public void avisoOcultaControlesInclusoSiEstabanAbiertosYPulsarloNoLosMuestra() throws Exception {
        llamar("entrarEnBotones");
        assertEquals(View.VISIBLE, vista("controles").getVisibility());
        actualizar(12000);
        assertEquals(View.GONE, vista("controles").getVisibility());
        assertEquals(View.GONE, vista("cabecera").getVisibility());
        tecla(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_UP);
        tecla(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_DPAD_UP);
        assertEquals(View.GONE, vista("controles").getVisibility());
        tecla(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_CENTER);
        tecla(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_DPAD_CENTER);
        assertEquals(View.GONE, vista("controles").getVisibility());
        llamar("mostrarControles");
        assertEquals(View.GONE, vista("controles").getVisibility());
        actualizar(31000);
        tecla(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_DOWN);
        assertEquals(View.VISIBLE, vista("controles").getVisibility());
    }

    @Test public void aparecePreseleccionadoYDesapareceAlSalir() throws Exception {
        actualizar(12000);
        assertEquals(View.VISIBLE, vista("saltar").getVisibility());
        assertSame(campo("avisos"), vista("saltar").getParent());
        assertEquals(View.GONE, vista("controles").getVisibility());
        assertEquals(View.GONE, vista("cabecera").getVisibility());
        assertTrue(vista("saltar").hasFocus());
        actualizar(31000);
        assertEquals(View.GONE, vista("saltar").getVisibility());
        assertFalse(vista("saltar").hasFocus());
        assertTrue(vista("raiz").hasFocus());
    }

    @Test public void mandoEnfocaYActivaSinAbrirControlesNiProcesarElSoltado() throws Exception {
        actualizar(12000);

        assertTrue(vista("saltar").hasFocus());
        assertEquals(View.GONE, vista("controles").getVisibility());
        tecla(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_CENTER);
        assertEquals(30000L, ((Long) campo("objetivoMs")).longValue());
        assertEquals(View.GONE, vista("saltar").getVisibility());
        tecla(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_DPAD_CENTER);
        assertEquals(View.GONE, vista("controles").getVisibility());
        assertFalse(((ExoPlayer) campo("player")).getPlayWhenReady());
    }

    @Test public void temporizadorOcultaControlesDuranteIntroConservandoFocoContextual() throws Exception {
        actualizar(12000);
        llamar("entrarEnBotones");
        assertTrue(vista("saltar").requestFocus());
        ExoPlayer original = (ExoPlayer) campo("player");
        ExoPlayer reproduciendo = (ExoPlayer) java.lang.reflect.Proxy.newProxyInstance(
                ExoPlayer.class.getClassLoader(), new Class<?>[] {ExoPlayer.class},
                (proxy, metodo, argumentos) -> {
                    if ("isPlaying".equals(metodo.getName())) return true;
                    throw new AssertionError("Llamada inesperada: " + metodo.getName());
                });
        poner("player", reproduciendo);
        try {
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper())
                    .idleFor(java.time.Duration.ofSeconds(4));
            assertEquals(View.GONE, vista("controles").getVisibility());
            assertEquals(View.GONE, vista("cabecera").getVisibility());
            assertEquals(View.VISIBLE, vista("saltar").getVisibility());
            assertTrue(vista("saltar").hasFocus());
        } finally {
            poner("player", original);
        }
    }

    @Test public void panelOcultaAvisoYLoRestauraSinRobarFoco() throws Exception {
        actualizar(12000);
        llamar("abrirPanel");
        assertEquals(View.GONE, vista("saltar").getVisibility());
        llamar("cerrarPanel");
        assertEquals(View.VISIBLE, vista("saltar").getVisibility());
        assertFalse(vista("saltar").hasFocus());
    }

    @Test public void creditosYFinalVirtualMantienenEtiquetas() throws Exception {
        List<JSONObject> capitulos = (List<JSONObject>) campo("capitulos");
        capitulos.clear();
        capitulos.add(new JSONObject().put("kind", "credits").put("start", 70).put("end", 100));
        actualizar(75000);
        assertEquals("Saltar créditos", ((TextView) campo("saltar")).getText().toString());
        poner("siguienteId", "episodio2");
        actualizar(75000);
        assertTrue(((TextView) campo("saltar")).getText().toString().startsWith("Siguiente episodio ("));
        capitulos.clear();
        poner("plan", new ReglasReproductor.Plan(new JSONObject().put("duration_ms", 100000)));
        long dur = (Long) llamar("duracionMs");
        assertTrue(dur > 60000);
        actualizar(dur - 20000);
        assertEquals(View.VISIBLE, vista("saltar").getVisibility());
        actualizar(dur);
        assertEquals(View.GONE, vista("saltar").getVisibility());
        actualizar(dur - 20000);
        assertEquals(View.VISIBLE, vista("saltar").getVisibility());
        actualizar(dur - 40000);
        assertEquals(View.GONE, vista("saltar").getVisibility());
    }

    @Test public void cancelarConMandoSoloAfectaEsteEpisodioYNoRobaFocoCadaTick() throws Exception {
        poner("plan", new ReglasReproductor.Plan(new JSONObject().put("duration_ms", 100000)));
        ((List<JSONObject>) campo("capitulos")).clear();
        ((List<JSONObject>) campo("capitulos")).add(new JSONObject().put("kind", "credits").put("start", 70).put("end", 100));
        poner("siguienteId", "episodio2");
        actualizar(75000);
        assertTrue(vista("saltar").hasFocus());
        assertEquals(85000L, ((Long) campo("siguienteLimite")).longValue());
        tecla(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_LEFT);
        assertTrue(vista("cancelarSiguiente").hasFocus());
        actualizar(76000);
        assertTrue(vista("cancelarSiguiente").hasFocus());
        tecla(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_CENTER);
        tecla(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_DPAD_CENTER);
        assertEquals(true, campo("siguienteCancelado"));
        assertEquals(View.GONE, vista("cancelarSiguiente").getVisibility());
        assertTrue(vista("saltar").hasFocus());
        assertEquals("Siguiente episodio", ((TextView) campo("saltar")).getText().toString());
    }

    private void tecla(int accion, int codigo) { app.dispatchKeyEvent(new KeyEvent(accion, codigo)); }
    private View vista(String nombre) throws Exception { return (View) campo(nombre); }
    private Object campo(String nombre) throws Exception {
        Field f = ReproductorActivity.class.getDeclaredField(nombre);
        f.setAccessible(true);
        return f.get(app);
    }
    private void poner(String nombre, Object valor) throws Exception {
        Field f = ReproductorActivity.class.getDeclaredField(nombre);
        f.setAccessible(true);
        f.set(app, valor);
    }
    private Object llamar(String nombre) throws Exception {
        Method m = ReproductorActivity.class.getDeclaredMethod(nombre);
        m.setAccessible(true);
        return m.invoke(app);
    }
    private void actualizar(long pos) throws Exception {
        Method m = ReproductorActivity.class.getDeclaredMethod("actualizarEspecial", long.class);
        m.setAccessible(true);
        m.invoke(app, pos);
    }
}
