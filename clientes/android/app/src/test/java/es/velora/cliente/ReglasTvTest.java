package es.velora.cliente;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertSame;
import static org.junit.Assert.assertTrue;

import org.json.JSONObject;
import org.junit.Test;

public class ReglasTvTest {
    @Test
    public void tresCierresNoBloqueanUnaVersionNueva() {
        assertTrue(ReglasTv.nativaPermitida(0, 0, 3));
        assertTrue(ReglasTv.nativaPermitida(1, 3, 3));
        assertFalse(ReglasTv.nativaPermitida(2, 3, 3));
        assertTrue("la versión nueva puede ser el arreglo", ReglasTv.nativaPermitida(2, 3, 4));
    }

    @Test
    public void atrasRecorreLasPantallasYLasSeccionesNoSeApilan() {
        ReglasTv.Historial<String> h = new ReglasTv.Historial<>("inicio");
        h.seccion("buscar");
        h.seccion("peliculas");
        h.abrir("ficha");
        assertEquals("peliculas", h.atras());
        assertEquals("inicio", h.atras());
        assertNull("en la raíz toca ofrecer salir", h.atras());
        assertSame("inicio", h.actual());
    }

    @Test
    public void volverAlInicioDesdeUnaFichaNoDejaNadaDebajo() {
        ReglasTv.Historial<String> h = new ReglasTv.Historial<>("inicio");
        h.abrir("ficha");
        h.seccion("inicio");
        assertEquals("inicio", h.actual());
        assertNull(h.atras());
    }

    @Test
    public void elInicioAdmiteListasNulasYQuitaFilasVacias() throws Exception {
        ReglasTv.Inicio i = ReglasTv.leerInicio("{\"featured\":null,\"continue\":null,\"rows\":[{\"library\":{\"name\":\"A\"},\"items\":null},"
                + "{\"library\":{\"name\":\"B\"},\"items\":[{\"id\":\"1\",\"title\":\"T\",\"year\":null,\"runtime_ms\":null,\"has_poster\":true}]}]}");
        assertTrue(i.destacadas.isEmpty());
        assertEquals(1, i.filas.size());
        assertEquals("B", i.filas.get(0).titulo);
        assertEquals(0, i.filas.get(0).items.get(0).anio);
    }

    @Test
    public void lasBibliotecasOcultasNoVanAlLateral() throws Exception {
        assertEquals(1, ReglasTv.leerBibliotecas("{\"libraries\":[{\"id\":\"1\",\"name\":\"A\",\"in_menu\":true},{\"id\":\"2\",\"name\":\"B\",\"in_menu\":false}]}").size());
    }

    @Test
    public void laFichaLeeArchivosAudiosYExtras() throws Exception {
        ReglasTv.Ficha f = ReglasTv.leerFicha("{\"item\":{\"id\":\"x\",\"title\":\"T\",\"year\":2020,\"runtime_ms\":7500000,\"official_rating\":\"12\",\"quality\":\"1080p\",\"tagline\":null,"
                + "\"genres\":[\"Drama\",\"Acción\"],\"position_ms\":0,\"files\":["
                + "{\"id\":\"f1\",\"missing\":true,\"version_label\":null,\"streams\":[]},"
                + "{\"id\":\"f2\",\"missing\":false,\"version_label\":null,\"streams\":[{\"index\":0,\"kind\":\"video\",\"height\":1080},{\"index\":1,\"kind\":\"audio\",\"language\":\"spa\",\"title\":null,\"codec\":\"ac3\",\"channels\":6}]}],"
                + "\"extras\":[{\"id\":\"e1\",\"kind\":\"trailer\",\"title\":\"\"}]}}");
        assertEquals("", f.lema);
        assertEquals("2020 · 2 h 5 min · 12 · 1080p · Drama, Acción", ReglasTv.datosBasicos(f));
        assertEquals("f2", ReglasTv.archivoInicial(f).id);
        assertEquals("1080p", ReglasTv.nombreVersion(f.archivos.get(1), 1));
        assertEquals("spa · AC3 · 6 canales", ReglasTv.nombreAudio(f.archivos.get(1).audios.get(0)));
        assertEquals("e1", ReglasTv.trailer(f));
        assertEquals("Tráiler", f.extras.get(0).nombre);
    }

    @Test
    public void lasRutasCifranLoQueEscribeElUsuario() {
        assertEquals("/api/v1/items?limit=60&q=la%20%26%20casa", ReglasTv.rutaBuscar(" la & casa "));
        assertEquals("/api/v1/items?library_id=b1&limit=60&offset=60&sort=year&order=reverse&genre=Ciencia%20ficci%C3%B3n&played=unplayed",
                ReglasTv.rutaBiblioteca("b1", 60, "year", true, "Ciencia ficción", "unplayed"));
        assertEquals("/api/v1/items/a/images/poster?v=1%2F2", ReglasTv.imagen("a", "poster", "1/2"));
    }

    @Test
    public void elReproductorRecibeLoMismoQueDesdeLaWeb() throws Exception {
        JSONObject j = new JSONObject(ReglasTv.datosReproductor("i", null, -1, true, "d"));
        assertEquals("i", j.getString("item"));
        assertEquals("", j.getString("file"));
        assertEquals(-1, j.getInt("audio"));
        assertTrue(j.getBoolean("inicio"));
        assertEquals("d", j.getString("device"));
    }

    @Test
    public void lasCredencialesVanPorPerfilOPorUsuario() throws Exception {
        assertEquals("p1", new JSONObject(ReglasTv.credenciales("p1", "", "x")).getString("user_id"));
        JSONObject m = new JSONObject(ReglasTv.credenciales(null, " ana ", "x"));
        assertEquals("ana", m.getString("username"));
        assertFalse(m.has("user_id"));
    }

    @Test
    public void elProgresoSoloSeEnseñaSiSePuedeContinuar() {
        ReglasTv.Tarjeta t = new ReglasTv.Tarjeta();
        t.duracionMs = 1000;
        t.posicionMs = 250;
        assertEquals(0.25f, ReglasTv.progreso(t), 0.001);
        t.vista = true;
        assertEquals(0f, ReglasTv.progreso(t), 0.001);
        assertFalse(t.reanudable());
    }

    @Test
    public void lasSeriesYSusEpisodiosAbrenLaFichaWeb() throws Exception {
        ReglasTv.Inicio r = ReglasTv.leerInicio("{\"featured\":[],\"rows\":[],"
                + "\"continue\":[{\"id\":\"e1\",\"kind\":\"episode\",\"title\":\"Piloto\",\"series_id\":\"s 1\",\"series_title\":\"Narcos\","
                + "\"season_number\":1,\"episode_number\":1,\"position_ms\":5}],"
                + "\"next_up\":[{\"id\":\"e2\",\"kind\":\"episode\",\"title\":\"Especial\",\"series_id\":\"s1\",\"series_title\":\"Serie\","
                + "\"season_number\":0,\"episode_number\":2,\"episode_end\":3}]}");
        ReglasTv.Tarjeta e = r.continuar.get(0);
        assertEquals("Narcos · T1 · E1 · Piloto", e.titulo);
        assertEquals("/serie/s%201?temporada=1", e.fichaWeb());
        assertEquals("la fila «Siguiente episodio» va antes de las novedades", "Siguiente episodio", r.filas.get(0).titulo);
        assertEquals("Serie · Especiales · E2–E3 · Especial", r.filas.get(0).items.get(0).titulo);
        assertEquals("/serie/s1?temporada=0", r.filas.get(0).items.get(0).fichaWeb());

        java.util.List<ReglasTv.Tarjeta> items = ReglasTv.leerItems("{\"items\":[{\"id\":\"s1\",\"kind\":\"series\",\"title\":\"Narcos\"},"
                + "{\"id\":\"m1\",\"kind\":\"movie\",\"title\":\"Origen\"},{\"id\":\"m2\",\"title\":\"Antigua\"}]}");
        assertTrue(items.get(0).esSerie());
        assertEquals("/serie/s1", items.get(0).fichaWeb());
        assertNull("una película sigue en la ficha nativa", items.get(1).fichaWeb());
        assertNull("un servidor anterior a E2 no manda kind: película", items.get(2).fichaWeb());
    }
}
