package es.velora.cliente;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import org.json.JSONObject;
import org.junit.Test;

public class ReglasReproductorTest {
    @Test public void videoConvertidoEsFlujoYSuRespaldoMantieneLimites() throws Exception {
        ReglasReproductor.Plan plan = new ReglasReproductor.Plan(new JSONObject().put("method", "transcode").put("url", "/api/v1/stream/files/F/transcode?ticket=test"));
        assertTrue(plan.reproducible());
        assertTrue(plan.flujo());
        JSONObject caps = new JSONObject().put("video", new org.json.JSONArray().put("h264")).put("max_bit_depths", new JSONObject().put("h264", 8));
        JSONObject copia = ReglasReproductor.capsFlujo(caps);
        assertEquals(8, copia.getJSONObject("max_bit_depths").getInt("h264"));
        assertTrue(copia.getBoolean("force_remux"));
    }

    @Test
    public void atrasTrasUnTrailerVuelveAntesDeLaFichaYNuncaAlVideo() {
        // Inicio → ficha → tráiler → ficha (el respaldo web la deja repetida): Atrás va al inicio, no al tráiler ni a la misma ficha.
        List<String> h = Arrays.asList("/", "/pelicula/a", "/ver/a?web=1&file=t", "/pelicula/a");
        assertEquals(-3, ReglasReproductor.pasosAtras(h, 3));
        // Inicio → biblioteca → ficha → tráiler (cerrado: queda delante) → Atrás desde la ficha va a la biblioteca.
        assertEquals(-1, ReglasReproductor.pasosAtras(Arrays.asList("/", "/biblioteca/1?genero=Drama", "/pelicula/a", "/ver/a"), 2));
        // Otra biblioteca con otro filtro sí es otra pantalla.
        assertEquals(-1, ReglasReproductor.pasosAtras(Arrays.asList("/biblioteca/1", "/biblioteca/1?genero=Drama"), 1));
        // Solo vídeos o la misma pantalla detrás: no hay adónde volver.
        assertEquals(0, ReglasReproductor.pasosAtras(Arrays.asList("/ver/a", "/pelicula/a", "/pelicula/a"), 2));
        assertEquals(0, ReglasReproductor.pasosAtras(Collections.singletonList("/"), 0));
        // Lo que no es Velora (null) tampoco cuenta.
        assertEquals(-2, ReglasReproductor.pasosAtras(Arrays.asList("/", null, "/pelicula/a"), 2));
    }

    @Test
    public void declaraLoQueElAparatoSabeDecodificar() throws Exception {
        JSONObject caps = ReglasReproductor.caps(Arrays.asList("video/avc", "VIDEO/HEVC", "audio/mp4a-latm", "audio/ac3", "video/raro"), Collections.singletonList("dts"));
        assertEquals("[\"h264\",\"hevc\"]", caps.getJSONArray("video").toString());
        assertEquals("[\"dts\",\"aac\",\"ac3\"]", caps.getJSONArray("audio").toString());
        assertTrue(caps.getJSONArray("containers").toString().contains("mkv"));
        assertTrue(caps.getBoolean("select_audio"));
    }

    @Test
    public void noRepiteLoQueTambienVaDirecto() throws Exception {
        JSONObject caps = ReglasReproductor.caps(Collections.singletonList("audio/ac3"), Collections.singletonList("ac3"));
        assertEquals(1, caps.getJSONArray("audio").length());
    }

    @Test
    public void leeElPlanDelServidor() throws Exception {
        ReglasReproductor.Plan directo = new ReglasReproductor.Plan(new JSONObject("{\"method\":\"direct_play\",\"audio_index\":1,\"url\":\"/api/v1/stream/files/f\",\"duration_ms\":5000}"));
        assertTrue(directo.reproducible());
        assertFalse(directo.flujo());
        assertEquals(1, directo.audio);
        ReglasReproductor.Plan flujo = new ReglasReproductor.Plan(new JSONObject("{\"method\":\"direct_stream\",\"url\":\"/r?audio=1\"}"));
        assertTrue(flujo.flujo());
        assertEquals("/r?audio=1&t=12.500&n=3", ReglasReproductor.urlFlujo(flujo.url, 12.5, 3));
        ReglasReproductor.Plan no = new ReglasReproductor.Plan(new JSONObject("{\"method\":\"unsupported\",\"reasons\":[\"Uno.\",\"Dos.\"]}"));
        assertFalse(no.reproducible());
        assertEquals("Uno. Dos.", no.motivo);
    }

    @Test
    public void unaUrlAjenaNoSeReproduce() throws Exception {
        assertFalse(new ReglasReproductor.Plan(new JSONObject("{\"method\":\"direct_play\",\"url\":\"http://otro/x\"}")).reproducible());
    }

    @Test
    public void decideDondeEmpezar() {
        assertEquals(9000, ReglasReproductor.inicioMs(false, 9000, false, false));
        assertEquals(0, ReglasReproductor.inicioMs(true, 9000, false, false));
        assertEquals(0, ReglasReproductor.inicioMs(false, 9000, true, false));
        assertEquals(0, ReglasReproductor.inicioMs(false, 9000, false, true));
    }

    @Test
    public void unSaltoNoSeSaleDeLaPelicula() {
        assertEquals(0, ReglasReproductor.limitarMs(-5000, 100000));
        assertEquals(99000, ReglasReproductor.limitarMs(500000, 100000));
        assertEquals(50000, ReglasReproductor.limitarMs(50000, 100000));
        assertEquals(50000, ReglasReproductor.limitarMs(50000, 0));
    }

    @Test
    public void distingueUnCorteDelFinal() {
        assertTrue(ReglasReproductor.finPrematuro(true, 50000, 100000));
        assertFalse(ReglasReproductor.finPrematuro(true, 97000, 100000));
        assertFalse(ReglasReproductor.finPrematuro(false, 50000, 100000));
    }

    @Test
    public void formatos() {
        assertEquals("0:09", ReglasReproductor.tiempo(9500));
        assertEquals("1:02:03", ReglasReproductor.tiempo(3723000));
        assertEquals("Español · AC3 · 5.1 · Cine", ReglasReproductor.etiquetaAudio("spa", "ac3", 6, "Cine"));
        assertEquals("Idioma sin indicar", ReglasReproductor.idioma("und"));
        assertEquals("xyz", ReglasReproductor.idioma("xyz"));
    }

    private static final ReglasReproductor.Causa CORTE = ReglasReproductor.Causa.TRANSPORTE;
    private static final ReglasReproductor.Causa FORMATO = ReglasReproductor.Causa.FORMATO;

    @Test
    public void unArchivoDirectoQueNoSeAbrePideFlujoUnaSolaVez() {
        ReglasReproductor.Recuperacion r = new ReglasReproductor.Recuperacion();
        assertFalse(r.flujoForzado());
        assertEquals(ReglasReproductor.Recuperacion.Accion.PEDIR_FLUJO, r.alFallar(false, 1000, FORMATO));
        assertTrue(r.flujoForzado());
        assertEquals(ReglasReproductor.Recuperacion.Accion.RESPALDO, r.alFallar(false, 1000, FORMATO));
    }

    @Test
    public void unCorteEnDirectoSeReintentaEnDirectoSinReempaquetar() {
        ReglasReproductor.Recuperacion r = new ReglasReproductor.Recuperacion();
        for (int i = 0; i < ReglasReproductor.Recuperacion.MAX_REINTENTOS; i++) {
            assertEquals(ReglasReproductor.Recuperacion.Accion.REINTENTAR, r.alFallar(false, 5000 + i * 1000, CORTE));
        }
        assertFalse(r.flujoForzado());
        assertEquals(ReglasReproductor.Recuperacion.Accion.RESPALDO, r.alFallar(false, 9000, CORTE));
    }

    @Test
    public void unFlujoQueFallaSinAvanzarAcabaEnElRespaldo() {
        ReglasReproductor.Recuperacion r = new ReglasReproductor.Recuperacion();
        for (int i = 0; i < ReglasReproductor.Recuperacion.MAX_REINTENTOS; i++) {
            assertEquals(ReglasReproductor.Recuperacion.Accion.REINTENTAR, r.alFallar(true, 5000 + i * 1000, CORTE));
        }
        assertEquals(ReglasReproductor.Recuperacion.Accion.RESPALDO, r.alFallar(true, 9000, CORTE));
        assertEquals(ReglasReproductor.Recuperacion.Accion.RESPALDO, r.alFallar(true, 9500, ReglasReproductor.Causa.OCUPADO));
        // Un formato que falla ya reempaquetado no vuelve a pedir el mismo flujo.
        assertEquals(ReglasReproductor.Recuperacion.Accion.RESPALDO, new ReglasReproductor.Recuperacion().alFallar(true, 1000, FORMATO));
    }

    @Test
    public void unVideoQueElAparatoNoDecodificaSeExplicaSinReempaquetar() {
        ReglasReproductor.Recuperacion r = new ReglasReproductor.Recuperacion();
        assertEquals(ReglasReproductor.Recuperacion.Accion.REINTENTAR, r.alFallar(false, 0, ReglasReproductor.Causa.VIDEO));
        assertEquals(ReglasReproductor.Recuperacion.Accion.PEDIR_VIDEO, r.alFallar(false, 0, ReglasReproductor.Causa.VIDEO));
        assertTrue(r.videoForzado());
        assertEquals(ReglasReproductor.Recuperacion.Accion.NO_COMPATIBLE, r.alFallar(true, 0, ReglasReproductor.Causa.VIDEO));
        assertFalse(r.flujoForzado());
    }

    /** Cualquier mezcla de fallos sin que la película avance termina siempre en pocas acciones: nunca un ciclo sin fin. */
    @Test
    public void cualquierMezclaDeFallosTerminaEnPocosIntentos() {
        ReglasReproductor.Causa[] causas = ReglasReproductor.Causa.values();
        java.util.Random azar = new java.util.Random(7);
        for (int ronda = 0; ronda < 500; ronda++) {
            ReglasReproductor.Recuperacion r = new ReglasReproductor.Recuperacion();
            boolean enFlujo = azar.nextBoolean();
            int acciones = 0;
            ReglasReproductor.Recuperacion.Accion a;
            do {
                a = r.alFallar(enFlujo, 1000, causas[azar.nextInt(causas.length)]);
                if (a == ReglasReproductor.Recuperacion.Accion.PEDIR_FLUJO) enFlujo = true;
                acciones++;
            } while (a == ReglasReproductor.Recuperacion.Accion.REINTENTAR || a == ReglasReproductor.Recuperacion.Accion.PEDIR_FLUJO);
            assertTrue("demasiados intentos: " + acciones, acciones <= ReglasReproductor.Recuperacion.MAX_REINTENTOS + 2);
        }
    }

    @Test
    public void lasCausasSeDistinguenPorCodigoYPista() {
        assertEquals(ReglasReproductor.Causa.TRANSPORTE, ReglasReproductor.causa(2001, 0, null)); // conexión fallida
        assertEquals(ReglasReproductor.Causa.TRANSPORTE, ReglasReproductor.causa(2002, 0, null)); // tiempo agotado
        assertEquals(ReglasReproductor.Causa.TRANSPORTE, ReglasReproductor.causa(2004, 502, null));
        assertEquals(ReglasReproductor.Causa.OCUPADO, ReglasReproductor.causa(2004, 503, null));
        assertEquals(ReglasReproductor.Causa.VIDEO, ReglasReproductor.causa(2004, 422, null));
        assertEquals(ReglasReproductor.Causa.FORMATO, ReglasReproductor.causa(3001, 0, null)); // contenedor no válido
        assertEquals(ReglasReproductor.Causa.FORMATO, ReglasReproductor.causa(4003, 0, "audio/eac3"));
        assertEquals(ReglasReproductor.Causa.VIDEO, ReglasReproductor.causa(4001, 0, "video/hevc"));
        assertEquals(ReglasReproductor.Causa.VIDEO, ReglasReproductor.causa(4005, 0, "video/avc"));
        assertTrue(ReglasReproductor.esperaMs(ReglasReproductor.Causa.OCUPADO, 1) > ReglasReproductor.esperaMs(CORTE, 1));
        assertTrue(ReglasReproductor.esperaMs(CORTE, 3) <= 3000);
    }

    @Test
    public void lasCapacidadesIncluyenLaAlturaMaximaPorCodec() throws Exception {
        java.util.Map<String, Integer> altos = new java.util.HashMap<>();
        altos.put("video/hevc", 1088);
        altos.put("video/avc", 2160);
        altos.put("audio/mp4a-latm", 0);
        JSONObject caps = ReglasReproductor.caps(Arrays.asList("video/hevc", "video/avc", "audio/mp4a-latm"), Collections.<String>emptyList(), altos);
        assertEquals(1088, caps.getJSONObject("max_heights").getInt("hevc"));
        assertEquals(2160, caps.getJSONObject("max_heights").getInt("h264"));
        assertEquals(2, caps.getJSONObject("max_heights").length());
        assertEquals(1088, ReglasReproductor.capsFlujo(caps).getJSONObject("max_heights").getInt("hevc"));
        assertFalse(ReglasReproductor.caps(Arrays.asList("video/avc"), Collections.<String>emptyList()).has("max_heights"));
    }

    @Test
    public void elFlujoDeRespaldoConservaCodecsCompatibles() throws Exception {
        JSONObject flujo = ReglasReproductor.capsFlujo(ReglasReproductor.caps(Arrays.asList("video/hevc", "audio/ac3"), Collections.emptyList()));
        assertTrue(flujo.getJSONArray("containers").toString().contains("mkv"));
        assertEquals("[\"ac3\"]", flujo.getJSONArray("audio").toString());
        assertTrue(flujo.getBoolean("force_remux"));
        assertEquals("[\"hevc\"]", flujo.getJSONArray("video").toString());
    }

    @Test
    public void reconoceElPerfilBloqueado() {
        // 423: el perfil espera su PIN; no es falta de permiso ni un fallo del vídeo que haya que reintentar.
        assertTrue(ReglasReproductor.perfilBloqueado(423));
        assertFalse(ReglasReproductor.sinPermiso(423));
        assertFalse(ReglasReproductor.perfilBloqueado(403));
        assertFalse(ReglasReproductor.perfilBloqueado(0));
    }

    @Test
    public void reconoceLaFaltaDePermiso() {
        assertTrue(ReglasReproductor.sinPermiso(401));
        assertTrue(ReglasReproductor.sinPermiso(404));
        assertFalse(ReglasReproductor.sinPermiso(500));
        assertFalse(ReglasReproductor.sinPermiso(0));
    }

    @Test
    public void reconocePantallasRaiz() {
        assertTrue(ReglasReproductor.pantallaRaiz("/"));
        assertTrue(ReglasReproductor.pantallaRaiz("/entrar"));
        assertFalse(ReglasReproductor.pantallaRaiz("/pelicula/1"));
        assertFalse(ReglasReproductor.pantallaRaiz("/biblioteca/1"));
    }

    @Test
    public void leeCookies() {
        assertEquals("abc", ReglasReproductor.cookie("velora_sesion=x; velora_csrf=abc", "velora_csrf"));
        assertNull(ReglasReproductor.cookie("otra=1", "velora_csrf"));
        assertNull(ReglasReproductor.cookie(null, "velora_csrf"));
    }

    @Test
    public void leeSubtitulos() {
        List<ReglasReproductor.Cue> cues = ReglasReproductor.leerVtt("WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.500 align:start\n<i>Hola</i>\nmundo\n\n01:00:00.000 --> 01:00:01.000\nFin\n\nmal --> peor\nNada\n");
        assertEquals(2, cues.size());
        assertEquals("Hola\nmundo", ReglasReproductor.cueActiva(cues, 1500));
        assertEquals("", ReglasReproductor.cueActiva(cues, 2500));
        assertEquals("Fin", ReglasReproductor.cueActiva(cues, 3600500));
    }
    @Test public void seleccionaForzadosEspanolSinElegirCompletosNiIngles() throws Exception {
        List<org.json.JSONObject> pistas = new java.util.ArrayList<>();
        pistas.add(new org.json.JSONObject().put("id","en").put("language","eng").put("forced",true).put("supported",true));
        pistas.add(new org.json.JSONObject().put("id","full").put("language","spa").put("supported",true));
        assertEquals("",ReglasReproductor.subtituloForzadoEspanol(pistas));
        pistas.add(new org.json.JSONObject().put("id","pgs").put("language","es-ES").put("forced",true));
        assertEquals("pgs",ReglasReproductor.subtituloForzadoEspanol(pistas));
        pistas.add(new org.json.JSONObject().put("id","es").put("language","spa").put("forced",true).put("supported",true));
        assertEquals("es",ReglasReproductor.subtituloForzadoEspanol(pistas));
    }

}
