package es.velora.cliente;

import static org.junit.Assert.*;
import android.graphics.Bitmap;
import android.graphics.drawable.BitmapDrawable;
import android.os.Looper;
import android.widget.ImageView;
import com.sun.net.httpserver.HttpServer;
import java.io.ByteArrayOutputStream;
import java.net.InetSocketAddress;
import java.util.Collections;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class ImagenesTest {
    @Test public void comparteDescargasYReutilizaCacheEntrePantallas() throws Exception {
        AtomicInteger requests = new AtomicInteger();
        Bitmap bitmap = Bitmap.createBitmap(64,64,Bitmap.Config.ARGB_8888);
        ByteArrayOutputStream png = new ByteArrayOutputStream(); bitmap.compress(Bitmap.CompressFormat.PNG,100,png);
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1",0),0);
        server.createContext("/logo", h -> {
            requests.incrementAndGet();
            assertTrue(h.getRequestURI().getQuery().contains("w=160"));
            byte[] bytes = png.toByteArray();h.sendResponseHeaders(200,bytes.length);h.getResponseBody().write(bytes);h.close();
        });server.start();
        Imagenes images = new Imagenes(), reopened = new Imagenes();
        try {
            String origin = "http://127.0.0.1:" + server.getAddress().getPort();
            images.sesion(origin, Collections.singletonMap("Cookie","test"));
            ImageView a = new ImageView(RuntimeEnvironment.getApplication()), b = new ImageView(RuntimeEnvironment.getApplication());
            String route="/logo?v=" + System.nanoTime();
            images.poner(a,route,100,100);images.poner(b,route,100,100);
            waitFor(a);waitFor(b);assertEquals(1,requests.get());
            assertTrue(((BitmapDrawable)a.getDrawable()).getBitmap().hasAlpha());
            reopened.sesion(origin,Collections.singletonMap("Cookie","test"));
            ImageView c = new ImageView(RuntimeEnvironment.getApplication());reopened.poner(c,route,100,100);waitFor(c);
            assertEquals("Disk cache should avoid a new HTTP request",1,requests.get());
        } finally {images.cerrar();reopened.cerrar();server.stop(0);}
    }
    private static void waitFor(ImageView image) throws Exception {
        long deadline=System.currentTimeMillis()+5000;
        while(image.getDrawable()==null&&System.currentTimeMillis()<deadline){Thread.sleep(20);Shadows.shadowOf(Looper.getMainLooper()).idle();}
        assertNotNull("Image did not load",image.getDrawable());
    }
}
