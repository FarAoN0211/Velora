package es.velora.cliente;

import static org.junit.Assert.assertArrayEquals;
import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertThrows;
import static org.junit.Assert.assertTrue;

import java.io.ByteArrayInputStream;
import java.io.File;
import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.util.Collections;
import java.util.Set;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Rule;
import org.junit.Test;
import org.junit.rules.TemporaryFolder;

public class ReglasOtaTest {
    private static final String HUELLA = "a".repeat(64);
    private static final Set<String> NUESTRA = Collections.singleton("nuestra");
    private static final Set<String> OTRA = Collections.singleton("otra");

    @Rule
    public TemporaryFolder carpeta = new TemporaryFolder();

    private static String anuncio(String version, String archivo, String huella) {
        return "{\"versionCode\":" + version + ",\"versionName\":\"0.2.0\",\"archivo\":\"" + archivo + "\",\"sha256\":\"" + huella + "\"}";
    }

    private static String huellaDe(byte[] datos) throws IOException {
        return ReglasOta.hex(ReglasOta.sha256().digest(datos));
    }

    @Test
    public void aceptaUnAnuncioValido() throws Exception {
        ReglasOta.Anuncio a = ReglasOta.leerAnuncio(anuncio("2", "velora-2.apk", HUELLA));
        assertEquals(2, a.version);
        assertEquals("0.2.0", a.nombre);
        assertEquals("velora-2.apk", a.archivo);
        assertTrue(a.esMasNuevoQue(1));
        assertFalse(a.esMasNuevoQue(2));
        assertFalse(a.esMasNuevoQue(3));
    }

    @Test
    public void rechazaAnunciosInvalidos() {
        String[] malos = {
            "no es json",
            "{}",
            anuncio("\"dos\"", "velora-2.apk", HUELLA),
            anuncio("0", "velora-2.apk", HUELLA),
            anuncio("-1", "velora-2.apk", HUELLA),
            anuncio("2", "../velora-2.apk", HUELLA),
            anuncio("2", "velora-2.apk/x", HUELLA),
            anuncio("2", ".apk", HUELLA),
            anuncio("2", "velora-2.exe", HUELLA),
            anuncio("2", "velora-2.apk", "corta"),
            anuncio("2", "velora-2.apk", "z".repeat(64)),
        };
        for (String m : malos) assertThrows(m, ReglasOta.ErrorOta.class, () -> ReglasOta.leerAnuncio(m));
    }

    @Test
    public void unNombreDeVersionRaroNoLlegaAlAviso() throws Exception {
        String json = "{\"versionCode\":2,\"versionName\":\"<b>hola</b>\",\"archivo\":\"velora-2.apk\",\"sha256\":\"" + HUELLA + "\"}";
        assertEquals("", ReglasOta.leerAnuncio(json).nombre);
    }

    @Test
    public void rechazaUnAnuncioDemasiadoGrande() throws Exception {
        assertArrayEquals(new byte[10], ReglasOta.leerAcotado(new ByteArrayInputStream(new byte[10]), 10));
        assertThrows(ReglasOta.ErrorOta.class, () -> ReglasOta.leerAcotado(new ByteArrayInputStream(new byte[11]), 10));
    }

    @Test
    public void guardaUnaDescargaCorrectaSinDejarTemporal() throws Exception {
        byte[] datos = "apk de prueba".getBytes("UTF-8");
        File apk = new File(carpeta.getRoot(), "velora.apk");
        ReglasOta.guardar(new ByteArrayInputStream(datos), datos.length, apk, 100, huellaDe(datos));
        assertArrayEquals(datos, Files.readAllBytes(apk.toPath()));
        assertEquals(1, carpeta.getRoot().list().length);
    }

    private void compruebaQueNoQuedaNada(long declarado, long maximo, String huella, byte[] datos) throws Exception {
        File apk = new File(carpeta.getRoot(), "velora.apk");
        assertTrue(apk.createNewFile()); // Lo que hubiera de un intento anterior tampoco debe sobrevivir.
        assertThrows(ReglasOta.ErrorOta.class, () -> ReglasOta.guardar(new ByteArrayInputStream(datos), declarado, apk, maximo, huella));
        assertEquals(0, carpeta.getRoot().list().length);
    }

    @Test
    public void unaDescargaCorruptaNoDejaArchivos() throws Exception {
        compruebaQueNoQuedaNada(-1, 100, HUELLA, "dañado".getBytes("UTF-8"));
    }

    @Test
    public void unaDescargaExcesivaSeCortaSinCabecera() throws Exception {
        byte[] datos = new byte[200];
        compruebaQueNoQuedaNada(-1, 100, huellaDe(datos), datos);
    }

    @Test
    public void unaDescargaExcesivaSeRechazaPorSuCabecera() throws Exception {
        File apk = new File(carpeta.getRoot(), "velora.apk");
        InputStream sinLeer = new InputStream() {
            @Override
            public int read() {
                throw new AssertionError("no debe empezar a descargar");
            }
        };
        assertThrows(ReglasOta.ErrorOta.class, () -> ReglasOta.guardar(sinLeer, ReglasOta.MAX_APK + 1, apk, ReglasOta.MAX_APK, HUELLA));
        assertEquals(0, carpeta.getRoot().list().length);
    }

    @Test
    public void unaDescargaCortadaNoDejaArchivos() throws Exception {
        byte[] datos = new byte[50];
        compruebaQueNoQuedaNada(80, 100, huellaDe(datos), datos);
    }

    @Test
    public void unErrorDeRedALaMitadNoDejaArchivos() throws Exception {
        File apk = new File(carpeta.getRoot(), "velora.apk");
        InputStream cortada = new InputStream() {
            private int n;

            @Override
            public int read() throws IOException {
                if (n++ < 10) return 0;
                throw new IOException("sin red");
            }
        };
        assertThrows(IOException.class, () -> ReglasOta.guardar(cortada, -1, apk, 100, HUELLA));
        assertEquals(0, carpeta.getRoot().list().length);
    }

    @Test
    public void aceptaElApkCorrecto() throws Exception {
        ReglasOta.comprobarApk("es.velora.cliente", "es.velora.cliente", 2, 2, 1, NUESTRA, NUESTRA);
    }

    @Test
    public void rechazaOtroPaquete() {
        assertThrows(ReglasOta.ErrorOta.class, () -> ReglasOta.comprobarApk("es.velora.cliente", "es.otra.app", 2, 2, 1, NUESTRA, NUESTRA));
        assertThrows(ReglasOta.ErrorOta.class, () -> ReglasOta.comprobarApk("es.velora.cliente", null, 2, 2, 1, NUESTRA, NUESTRA));
    }

    @Test
    public void rechazaVersionDistintaDeLaAnunciadaOAntigua() {
        assertThrows(ReglasOta.ErrorOta.class, () -> ReglasOta.comprobarApk("p", "p", 3, 2, 1, NUESTRA, NUESTRA));
        assertThrows(ReglasOta.ErrorOta.class, () -> ReglasOta.comprobarApk("p", "p", 1, 1, 1, NUESTRA, NUESTRA));
        assertThrows(ReglasOta.ErrorOta.class, () -> ReglasOta.comprobarApk("p", "p", 1, 1, 2, NUESTRA, NUESTRA));
    }

    @Test
    public void rechazaOtraFirma() {
        assertThrows(ReglasOta.ErrorOta.class, () -> ReglasOta.comprobarApk("p", "p", 2, 2, 1, OTRA, NUESTRA));
    }

    @Test
    public void sinFirmaDelArchivoDecideAndroid() throws Exception {
        ReglasOta.comprobarApk("p", "p", 2, 2, 1, Collections.emptySet(), NUESTRA);
    }

    @Test
    public void soloUnaOperacionALaVez() throws Exception {
        ReglasOta.Turno turno = new ReglasOta.Turno();
        AtomicInteger conTurno = new AtomicInteger();
        CountDownLatch salida = new CountDownLatch(1), fin = new CountDownLatch(16);
        for (int i = 0; i < 16; i++) {
            new Thread(() -> {
                try {
                    salida.await();
                    if (turno.tomar()) conTurno.incrementAndGet();
                } catch (InterruptedException ignorada) {
                    Thread.currentThread().interrupt();
                }
                fin.countDown();
            }).start();
        }
        salida.countDown();
        fin.await();
        assertEquals(1, conTurno.get());
        assertFalse(turno.tomar());
        turno.soltar();
        assertTrue(turno.tomar());
    }

    @Test
    public void unFalloSeReintentaAntesQueUnAcierto() {
        ReglasOta.Plazo plazo = new ReglasOta.Plazo();
        assertTrue(plazo.toca(0)); // Recién encendido el aparato también toca.
        plazo.fallo(1000);
        assertFalse(plazo.toca(1000 + ReglasOta.Plazo.REINTENTO - 1));
        assertTrue(plazo.toca(1000 + ReglasOta.Plazo.REINTENTO));
        plazo.acierto(2000);
        assertFalse(plazo.toca(2000 + ReglasOta.Plazo.REINTENTO));
        assertTrue(plazo.toca(2000 + ReglasOta.Plazo.CADA));
    }

    @Test
    public void elDiagnosticoDiceEnQuePasoFalloYQueRespondioAndroid() {
        assertEquals("Fallo en «cerrar tras confirmar la sesión»: IOException: write failed: EBADF",
                DiagnosticoOta.describirFallo("cerrar tras confirmar la sesión", new java.io.IOException("write failed: EBADF")));
        assertEquals("Fallo en «copiar el APK»: IllegalStateException", DiagnosticoOta.describirFallo("copiar el APK", new IllegalStateException()));
        assertEquals("Respuesta del instalador: cancelada o abandonada (3): Session was abandoned", DiagnosticoOta.describirEstado(3, "Session was abandoned"));
        assertEquals("Respuesta del instalador: sin espacio (6)", DiagnosticoOta.describirEstado(6, null));
        assertEquals("Respuesta del instalador: estado desconocido (99)", DiagnosticoOta.describirEstado(99, ""));
    }

    @Test
    public void laBitacoraConservaLaExcepcionYLaRespuestaPosteriorEnOrden() {
        String b = DiagnosticoOta.anadir("[]", 1, 7, 42, 0, "Fallo en «cerrar»: IOException", true);
        b = DiagnosticoOta.anadir(b, 2, 7, 42, 0, "La app ha abandonado la sesión", false);
        b = DiagnosticoOta.anadir(b, 3, 7, 42, 0, "Respuesta del instalador: cancelada o abandonada (3)", true);
        String t = DiagnosticoOta.texto(b);
        assertTrue(t, t.indexOf("IOException") >= 0);
        assertTrue(t, t.indexOf("IOException") < t.indexOf("ha abandonado"));
        assertTrue(t, t.indexOf("ha abandonado") < t.indexOf("(3)"));
        assertTrue(t, t.contains("intento 7, sesión 42"));
        assertEquals(3, DiagnosticoOta.falloMasNuevo(b));
    }

    @Test
    public void laBitacoraEnseñaElUltimoIntentoFallidoYNoCreceSinFin() throws Exception {
        String b = DiagnosticoOta.anadir("[]", 1, 1, 10, 0, "fallo del primero", true);
        b = DiagnosticoOta.anadir(b, 2, 2, 20, 0, "fallo del segundo", true);
        assertFalse(DiagnosticoOta.texto(b).contains("primero"));
        assertTrue(DiagnosticoOta.texto(b).contains("segundo"));
        for (int i = 3; i < 200; i++) b = DiagnosticoOta.anadir(b, i, 3, 30, 0, "x", false);
        assertEquals(DiagnosticoOta.MAX_ENTRADAS, new org.json.JSONArray(b).length());
    }

    @Test
    public void unaBitacoraDanadaNoEstorba() {
        assertEquals(0, DiagnosticoOta.falloMasNuevo("no es json"));
        assertEquals("", DiagnosticoOta.texto("no es json"));
        assertEquals(1, DiagnosticoOta.falloMasNuevo(DiagnosticoOta.anadir("no es json", 1, 1, 1, 0, "x", true)));
    }
}
