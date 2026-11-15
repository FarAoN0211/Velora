package es.velora.cliente;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;
import static org.junit.Assert.fail;

import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.After;
import org.junit.Before;
import org.junit.Test;

/** Peticiones cancelables con un servidor local que responde despacio, como un origen remoto lento. */
public class ApiVeloraTest {
    private HttpServer servidor;
    private ApiVelora api;
    private final AtomicInteger peticiones = new AtomicInteger();

    @Before
    public void arrancar() throws IOException {
        servidor = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        servidor.setExecutor(Executors.newCachedThreadPool());
        servidor.createContext("/lento", h -> {
            peticiones.incrementAndGet();
            try {
                Thread.sleep(5000);
            } catch (InterruptedException ignorada) {
                Thread.currentThread().interrupt();
            }
            responder(h, "{\"chapters\":[]}");
        });
        servidor.createContext("/rapido", h -> {
            peticiones.incrementAndGet();
            responder(h, "{\"method\":\"direct_play\"}");
        });
        servidor.start();
        api = new ApiVelora("http://127.0.0.1:" + servidor.getAddress().getPort(), "", "");
    }

    private static void responder(com.sun.net.httpserver.HttpExchange h, String cuerpo) throws IOException {
        byte[] b = cuerpo.getBytes(StandardCharsets.UTF_8);
        h.sendResponseHeaders(200, b.length);
        try (OutputStream out = h.getResponseBody()) {
            out.write(b);
        }
    }

    @After
    public void parar() {
        servidor.stop(0);
    }

    @Test public void subtitulosProgresivosLleganAntesDelFin() throws Exception {
        java.util.concurrent.CountDownLatch liberar = new java.util.concurrent.CountDownLatch(1);
        java.util.concurrent.CountDownLatch recibida = new java.util.concurrent.CountDownLatch(1);
        servidor.createContext("/subtitulos", h -> {
            h.sendResponseHeaders(200, 0);
            try (OutputStream out = h.getResponseBody()) {
                out.write("WEBVTT\n\n00:00:01.000 --> 00:00:03.000\nFrase española\n\n".getBytes(StandardCharsets.UTF_8));
                out.flush();
                liberar.await(5, TimeUnit.SECONDS);
            } catch (InterruptedException e) { Thread.currentThread().interrupt(); }
        });
        ExecutorService hilo = Executors.newSingleThreadExecutor();
        try {
            Future<?> tarea = hilo.submit(() -> {
                try {api.getProgresivo("/subtitulos", new ApiVelora.Cancelable(), texto -> {
                    if (texto.contains("Frase española")) recibida.countDown();
                });} catch (IOException e) {throw new RuntimeException(e);}
            });
            assertTrue(recibida.await(2, TimeUnit.SECONDS));
            assertFalse("no debe esperar al cierre de la respuesta", tarea.isDone());
            liberar.countDown();
            tarea.get(2, TimeUnit.SECONDS);
        } finally {liberar.countDown();hilo.shutdownNow();}
    }

    @Test
    public void cancelarCortaLaEsperaAlMomento() throws Exception {
        ApiVelora.Cancelable control = new ApiVelora.Cancelable();
        ExecutorService hilo = Executors.newSingleThreadExecutor();
        long inicio = System.nanoTime();
        Future<String> f = hilo.submit(() -> api.get("/lento", control, 20000));
        Thread.sleep(200);
        control.cancelar();
        try {
            f.get(2, TimeUnit.SECONDS);
            fail("una petición cancelada no debería devolver respuesta");
        } catch (java.util.concurrent.ExecutionException esperada) {
            assertTrue(esperada.getCause() instanceof IOException);
        }
        assertTrue("debería acabar mucho antes de los 5 s del servidor", System.nanoTime() - inicio < TimeUnit.MILLISECONDS.toNanos(2000));
        hilo.shutdownNow();
    }

    @Test
    public void unaPeticionYaCanceladaNoLlegaAlServidor() throws Exception {
        ApiVelora.Cancelable control = new ApiVelora.Cancelable();
        control.cancelar();
        try {
            api.get("/rapido", control, 1000);
            fail("no debería enviarse");
        } catch (IOException esperada) {
            assertEquals(0, peticiones.get());
        }
    }

    /** Como en el reproductor: las listas lentas van en su hilo y la decisión en el suyo; la decisión no las espera, y al cerrar se cancelan. */
    @Test
    public void unaListaLentaNoRetrasaLaDecision() throws Exception {
        ExecutorService auxiliar = Executors.newSingleThreadExecutor();
        ExecutorService fondo = Executors.newSingleThreadExecutor();
        ApiVelora.Cancelable listas = new ApiVelora.Cancelable();
        long inicio = System.nanoTime();
        Future<String> capitulos = auxiliar.submit(() -> api.get("/lento", listas, 20000));
        String plan = fondo.submit(() -> api.enviar("POST", "/rapido", "{}", new ApiVelora.Cancelable())).get(2, TimeUnit.SECONDS);
        long decision = System.nanoTime() - inicio;
        assertTrue(plan.contains("direct_play"));
        assertTrue("la decisión no debería esperar a la lista lenta: " + decision / 1_000_000 + " ms", decision < TimeUnit.MILLISECONDS.toNanos(1000));
        assertFalse(capitulos.isDone());
        listas.cancelar(); // salir del reproductor
        try {
            capitulos.get(2, TimeUnit.SECONDS);
            fail("la lista cancelada no debería llegar");
        } catch (java.util.concurrent.ExecutionException esperada) {
            assertTrue(esperada.getCause() instanceof IOException);
        }
        auxiliar.shutdownNow();
        fondo.shutdownNow();
    }
}
