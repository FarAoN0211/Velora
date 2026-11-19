package es.velora.cliente;

import android.view.View;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;
import org.robolectric.util.ReflectionHelpers;
import static org.junit.Assert.*;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 34)
public class MainActivityFullscreenTest {
    private void activar(MainActivity app, boolean activa) throws Exception {
        for (Class<?> tipo : MainActivity.class.getDeclaredClasses()) {
            if (!tipo.getSimpleName().equals("Puente")) continue;
            java.lang.reflect.Constructor<?> constructor = tipo.getDeclaredConstructor(MainActivity.class);
            constructor.setAccessible(true);
            Object puente = constructor.newInstance(app);
            java.lang.reflect.Method metodo = tipo.getDeclaredMethod("pantallaCompletaReproductor", boolean.class);
            metodo.setAccessible(true);
            metodo.invoke(puente, activa);
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            return;
        }
        fail("Puente no encontrado");
    }
    @Test public void movilAbreLaMismaActividadNativaQueTv() throws Exception {
        MainActivity app = Robolectric.buildActivity(MainActivity.class).get();
        ReflectionHelpers.setField(app, "origen", "https://velora.example");
        ReflectionHelpers.setField(app, "esTv", false);
        Class<?> tipo = java.util.Arrays.stream(MainActivity.class.getDeclaredClasses())
                .filter(c -> c.getSimpleName().equals("Puente")).findFirst().get();
        java.lang.reflect.Constructor<?> constructor = tipo.getDeclaredConstructor(MainActivity.class);
        constructor.setAccessible(true);
        Object puente = constructor.newInstance(app);
        java.lang.reflect.Method metodo = tipo.getDeclaredMethod("reproducir", String.class);
        metodo.setAccessible(true);
        assertEquals(true, metodo.invoke(puente, "{\"item\":\"episodio\"}"));
        org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
        android.content.Intent intent = org.robolectric.Shadows.shadowOf(app).getNextStartedActivity();
        assertEquals(ReproductorActivity.class.getName(), intent.getComponent().getClassName());
        assertEquals("{\"item\":\"episodio\"}", intent.getStringExtra(ReproductorActivity.DATOS));
    }

    @Test public void movilEntraSinGestoYRestauraBarrasAlSalir() throws Exception {
        MainActivity app = Robolectric.buildActivity(MainActivity.class).get();
        activar(app, true);
        assertTrue((app.getWindow().getDecorView().getSystemUiVisibility() & View.SYSTEM_UI_FLAG_FULLSCREEN) != 0);
        assertTrue((app.getWindow().getDecorView().getSystemUiVisibility() & View.SYSTEM_UI_FLAG_HIDE_NAVIGATION) != 0);
        activar(app, false);
        assertEquals(View.SYSTEM_UI_FLAG_VISIBLE, app.getWindow().getDecorView().getSystemUiVisibility());
    }
    @Test public void tvConservaPantallaCompletaAlSalirDelReproductor() throws Exception {
        MainActivity app = Robolectric.buildActivity(MainActivity.class).get();
        ReflectionHelpers.setField(app, "esTv", true);
        activar(app, true);
        activar(app, false);
        assertTrue((app.getWindow().getDecorView().getSystemUiVisibility() & View.SYSTEM_UI_FLAG_FULLSCREEN) != 0);
    }
}
