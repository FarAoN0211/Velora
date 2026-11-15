package es.velora.cliente;
import static org.junit.Assert.*;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;
@RunWith(RobolectricTestRunner.class)
@Config(sdk=34)
public class VinculacionTest {
 private final String base="http://192.168.1.10:8640";
 @Test public void acceptsOnlyConfiguredServerAndSixDigits(){
  assertEquals("/vincular?codigo=012345",Vinculacion.ruta(base,base+"/vincular?codigo=012345"));
  assertEquals("/vincular?codigo=012345",Vinculacion.ruta(base,"velora://vincular?servidor=http%3A%2F%2F192.168.1.10%3A8640&codigo=012345"));
  for(String s:new String[]{"http://evil.local/vincular?codigo=012345",base+"/ver/123?codigo=012345",base+"/vincular?codigo=123",base+"/vincular?codigo=1234567","javascript:alert(1)","velora://vincular?servidor=http://evil.local&codigo=012345",base+"/vincular?codigo=012345#x"})assertNull(s,Vinculacion.ruta(base,s));
 }
}
