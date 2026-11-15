package es.velora.cliente;

import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.drawable.Drawable;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import static org.junit.Assert.*;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class IconosReproductorTest {
    @Test public void playYPausaDibujanUnSimboloVisible() {
        for (int icono : new int[] {R.drawable.ic_play_relleno, R.drawable.ic_pause_relleno}) {
            Drawable dibujo = RuntimeEnvironment.getApplication().getDrawable(icono);
            Bitmap bitmap = Bitmap.createBitmap(48,48,Bitmap.Config.ARGB_8888);
            dibujo.setBounds(0,0,48,48);
            dibujo.draw(new Canvas(bitmap));
            int pixeles = 0;
            for (int y=0;y<48;y++) for (int x=0;x<48;x++) if ((bitmap.getPixel(x,y) >>> 24) > 0) pixeles++;
            assertTrue("El icono debe dibujar su símbolo, no quedar vacío", pixeles > 100);
        }
    }
}
