package es.velora.cliente;

import android.net.Uri;

/** A QR/deep link can select a code on the configured server, never replace that
 * server or load arbitrary content into the authenticated WebView. */
final class Vinculacion {
    static String ruta(String origen, String texto) {
        try {
            Uri u=Uri.parse(texto);
            if(u.getUserInfo()!=null||u.getFragment()!=null)return null;
            String codigo=u.getQueryParameter("codigo");
            if(codigo==null||!codigo.matches("[0-9]{6}"))return null;
            if("velora".equals(u.getScheme())&&"vincular".equals(u.getHost())) {
                if(!origen.equals(u.getQueryParameter("servidor")))return null;
            } else if(!Servidor.mismoOrigen(origen,u)||!"/vincular".equals(u.getPath()))return null;
            return "/vincular?codigo="+codigo;
        } catch(RuntimeException invalido){return null;}
    }
}
