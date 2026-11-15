package es.velora.cliente;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertNull;

import org.junit.Test;

/** Direcciones del servidor: http solo en la red local; https en cualquier sitio. */
public class ServidorTest {
    @Test
    public void laRedLocalSigueFuncionandoComoAntes() {
        assertEquals("http://192.168.1.10:8640", Servidor.normalizar("192.168.1.10:8640"));
        assertEquals("http://192.168.1.10:8640", Servidor.normalizar(" http://192.168.1.10:8640/ "));
        assertEquals("http://10.0.0.5", Servidor.normalizar("10.0.0.5"));
        assertEquals("http://172.20.1.2:8640", Servidor.normalizar("172.20.1.2:8640"));
        assertEquals("http://biblioteca.local:8640", Servidor.normalizar("biblioteca.local:8640"));
        assertEquals("http://servidor:8640", Servidor.normalizar("servidor:8640"));
        assertEquals("https://192.168.1.10:8443", Servidor.normalizar("https://192.168.1.10:8443"));
    }

    @Test
    public void unaDireccionHttpsDeFueraSeAdmite() {
        assertEquals("https://velora.ejemplo.com", Servidor.normalizar("https://velora.ejemplo.com"));
        assertEquals("https://velora.ejemplo.com", Servidor.normalizar("HTTPS://Velora.Ejemplo.com/"));
        assertEquals("https://velora.ejemplo.com:8443", Servidor.normalizar("https://velora.ejemplo.com:8443"));
        assertEquals("sin esquema, fuera de casa va cifrada", "https://velora.ejemplo.com", Servidor.normalizar("velora.ejemplo.com"));
        assertEquals("https://81.40.12.7", Servidor.normalizar("https://81.40.12.7"));
    }

    @Test
    public void nadaSinCifrarSaleDeCasa() {
        assertNull(Servidor.normalizar("http://velora.ejemplo.com"));
        assertNull(Servidor.normalizar("http://81.40.12.7:8640"));
        assertNull(Servidor.normalizar("http://172.32.0.1"));
    }

    @Test
    public void direccionesQueNoFuncionaríanSeRechazan() {
        assertNull(Servidor.normalizar(""));
        assertNull(Servidor.normalizar("ftp://velora.ejemplo.com"));
        assertNull("Velora va en la raíz", Servidor.normalizar("https://ejemplo.com/velora"));
        assertNull(Servidor.normalizar("https://usuario:clave@ejemplo.com"));
        assertNull(Servidor.normalizar("https://ejemplo.com/?a=1"));
        assertNull(Servidor.normalizar("https://ejem plo.com"));
        assertNull(Servidor.normalizar("https://-ejemplo.com"));
        assertNull(Servidor.normalizar("https://ejemplo.com:0"));
    }
}
