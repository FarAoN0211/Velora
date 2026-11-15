package es.velora.cliente;
import android.app.AlertDialog;
import android.content.Context;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;
import org.robolectric.shadows.ShadowAlertDialog;
import static org.junit.Assert.*;
import static org.robolectric.Shadows.shadowOf;
@RunWith(RobolectricTestRunner.class)
@Config(sdk=28)
public class ServidorInicialTest {
 @Test public void instalacionNuevaPideServidorSinUsarUnoPersonal(){
  Context context=RuntimeEnvironment.getApplication();
  context.getSharedPreferences("velora",Context.MODE_PRIVATE).edit().clear().commit();
  assertEquals("",Servidor.leer(context));
  MainActivity app=Robolectric.buildActivity(MainActivity.class).setup().get();
  AlertDialog dialog=ShadowAlertDialog.getLatestAlertDialog();
  assertNotNull(dialog);assertTrue(dialog.isShowing());
  assertEquals("Dirección del servidor",shadowOf(dialog).getTitle().toString());
  assertFalse(app.isFinishing());
 }
 @Test public void actualizarConservaElServidorElegido(){
  Context context=RuntimeEnvironment.getApplication();
  Servidor.guardar(context,"http://biblioteca.local:8640");
  assertEquals("http://biblioteca.local:8640",Servidor.leer(context));
 }
}
