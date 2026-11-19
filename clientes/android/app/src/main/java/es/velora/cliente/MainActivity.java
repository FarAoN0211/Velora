package es.velora.cliente;

import android.app.Activity;
import com.google.zxing.integration.android.IntentIntegrator;
import com.google.zxing.integration.android.IntentResult;
import android.app.AlertDialog;
import android.app.UiModeManager;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.content.res.Configuration;
import android.graphics.Color;
import android.net.Uri;
import android.os.Bundle;
import android.text.InputType;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowManager;
import android.view.inputmethod.InputMethodManager;
import android.webkit.CookieManager;
import android.webkit.JavascriptInterface;
import android.webkit.RenderProcessGoneDetail;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.Toast;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;

/**
 * En todos los aparatos, el cliente es la propia web de Velora: la app solo añade lo que un navegador no da (pantalla completa, actualizaciones).
 * En TV la web se compone para el mando y los vídeos van al reproductor propio ({@link ReproductorActivity}).
 */
public final class MainActivity extends Activity {
    /** Abre esta ruta (la usaba la pantalla nativa de TV, que ya no se abre) y, al salir de ella, cierra esta pantalla. */
    static final String RUTA = "ruta";
    private static final int ELEGIR_ARCHIVO = 1;
    private static final int REPRODUCIR = 2;
    private String ruta;
    private FrameLayout raiz;
    private WebView web;
    private View videoCompleto;
    private WebChromeClient.CustomViewCallback salirVideo;
    private ValueCallback<Uri[]> archivoElegido;
    private Actualizador actualizador;
    private String origen;
    private String navegacion;
    private boolean esTv;
    private boolean reproductorCompleto;
    private long atrasPulsado;

    @Override
    protected void onCreate(Bundle guardado) {
        super.onCreate(guardado);
        origen = Servidor.leer(this);
        UiModeManager modo = (UiModeManager) getSystemService(UI_MODE_SERVICE);
        esTv = (modo != null && modo.getCurrentModeType() == Configuration.UI_MODE_TYPE_TELEVISION)
                || getPackageManager().hasSystemFeature(PackageManager.FEATURE_LEANBACK)
                || !getPackageManager().hasSystemFeature(PackageManager.FEATURE_TOUCHSCREEN);
        ruta = getIntent().getStringExtra(RUTA);
        // Decisión del usuario (7-11-2026): también en TV se arranca la web, que ya se compone para TV (VeloraTV); TvActivity no se abre.
        actualizador = new Actualizador(this);
        navegacion = leerAsset("navegacion.js");
        raiz = new FrameLayout(this);
        raiz.setBackgroundColor(Color.BLACK);
        setContentView(raiz);
        crearWeb();
        if (origen.isEmpty()) { cambiarServidor(); return; }
        String vinculacion=Vinculacion.ruta(origen,getIntent().getDataString());
        if(vinculacion!=null)ruta=vinculacion;
        if (vinculacion != null || guardado == null || web.restoreState(guardado) == null) web.loadUrl(origen + (ruta == null ? "/" : ruta));
    }

    private void crearWeb() {
        web = new WebView(this);
        web.setBackgroundColor(Color.BLACK);
        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setMediaPlaybackRequiresUserGesture(false);
        s.setTextZoom(100);
        s.setAllowFileAccess(false);
        s.setAllowContentAccess(false);
        s.setSupportMultipleWindows(false);
        // La web lo lee antes de pintar nada: en TV se compone para el mando desde el primer momento.
        s.setUserAgentString(s.getUserAgentString() + " VeloraCliente" + (esTv ? " VeloraTV" : ""));
        CookieManager.getInstance().setAcceptCookie(true);
        web.addJavascriptInterface(new Puente(), "VeloraApp");
        web.setWebViewClient(new Cliente());
        web.setWebChromeClient(new Cromo());
        web.setFocusable(true);
        web.setFocusableInTouchMode(true);
        // Sin nada que recuerde a un navegador: ni barras de desplazamiento, ni rebote, ni selección de texto.
        web.setOverScrollMode(View.OVER_SCROLL_NEVER);
        web.setVerticalScrollBarEnabled(false);
        web.setHorizontalScrollBarEnabled(false);
        web.setOnLongClickListener(v -> true);
        web.setLongClickable(false);
        raiz.addView(web, new ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        web.requestFocus();
    }

    @Override protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);setIntent(intent);
        if(intent.getData()!=null)abrirVinculacion(intent.getDataString());
    }
    private void abrirVinculacion(String texto) {
        String destino=Vinculacion.ruta(origen,texto);
        if(destino!=null)web.loadUrl(origen+destino);
        else Toast.makeText(this,"El QR no corresponde a este servidor o no contiene un código válido.",Toast.LENGTH_LONG).show();
    }

    /** Solo lo indispensable para la página, que es el servidor propio. */
    private final class Puente {
        @JavascriptInterface
        public void escanearVinculacion() {
            if(esTv)return;
            runOnUiThread(()->new IntentIntegrator(MainActivity.this)
                    .setDesiredBarcodeFormats(IntentIntegrator.QR_CODE)
                    .setPrompt("Escanea el QR de Velora que muestra el televisor")
                    .setBeepEnabled(false).setOrientationLocked(false).initiateScan());
        }

        @JavascriptInterface
        public void mantenerPantalla(boolean encendida) {
            runOnUiThread(() -> {
                if (encendida) getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
                else getWindow().clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
            });
        }

        /** El mismo reproductor Media3 en TV, móvil y tablet; la web queda como respaldo. */
        @JavascriptInterface
        public boolean reproducir(String datos) {
            runOnUiThread(() -> {
                Intent i = new Intent(MainActivity.this, ReproductorActivity.class);
                i.putExtra(ReproductorActivity.DATOS, datos);
                i.putExtra(ReproductorActivity.ORIGEN, origen);
                i.putExtra(ReproductorActivity.COOKIES, CookieManager.getInstance().getCookie(origen));
                startActivityForResult(i, REPRODUCIR);
            });
            return true;
        }

        @JavascriptInterface
        public void pantallaCompletaReproductor(boolean activa) {
            runOnUiThread(() -> {
                reproductorCompleto = activa;
                pantallaCompleta(esTv || reproductorCompleto || videoCompleto != null);
            });
        }

        @JavascriptInterface
        public void mostrarTeclado() {
            runOnUiThread(() -> {
                InputMethodManager teclado = (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
                web.requestFocus();
                if (teclado != null) teclado.showSoftInput(web, 0);
            });
        }
    }

    private final class Cliente extends WebViewClient {
        @Override
        public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest r) {
            if (Servidor.mismoOrigen(origen, r.getUrl())) return false;
            // Lo que no es Velora (la ficha de TMDB, por ejemplo) se abre fuera: la app nunca da su puente a otra página.
            try {
                startActivity(new Intent(Intent.ACTION_VIEW, r.getUrl()));
            } catch (RuntimeException sinNavegador) {
                Toast.makeText(MainActivity.this, "Este aparato no puede abrir ese enlace.", Toast.LENGTH_SHORT).show();
            }
            return true;
        }

        @Override
        public void onPageFinished(WebView v, String url) {
            if (Servidor.mismoOrigen(origen, Uri.parse(url))) {
                v.evaluateJavascript("window.__VELORA_TV=" + esTv + ";" + navegacion, null);
            }
            if (!origen.isEmpty()) actualizador.comprobar(origen);
        }

        @Override
        public void doUpdateVisitedHistory(WebView v, String url, boolean recarga) {
            // El reproductor alternativo se abrió desde la app: al salir del vídeo se vuelve a ella, no a la web.
            if (ruta != null && ruta.startsWith("/ver/") && !Uri.parse(url).getPath().startsWith("/ver/")) finish();
        }

        @Override
        public void onReceivedError(WebView v, WebResourceRequest r, WebResourceError e) {
            if (r.isForMainFrame()) sinConexion();
        }

        @Override
        public boolean onRenderProcessGone(WebView v, RenderProcessGoneDetail d) {
            // El motor de la web se ha cerrado (poca memoria en la TV Box): se vuelve a crear en vez de cerrar la app.
            raiz.removeView(web);
            web.destroy();
            crearWeb();
            web.loadUrl(origen + "/");
            return true;
        }
    }

    private final class Cromo extends WebChromeClient {
        @Override
        public void onShowCustomView(View vista, CustomViewCallback salir) {
            videoCompleto = vista;
            salirVideo = salir;
            raiz.addView(vista, new ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
            web.setVisibility(View.GONE);
            pantallaCompleta(true);
        }

        @Override
        public void onHideCustomView() {
            if (videoCompleto == null) return;
            raiz.removeView(videoCompleto);
            videoCompleto = null;
            web.setVisibility(View.VISIBLE);
            pantallaCompleta(esTv || reproductorCompleto);
            web.requestFocus();
        }

        @Override
        public boolean onShowFileChooser(WebView v, ValueCallback<Uri[]> respuesta, FileChooserParams params) {
            if (archivoElegido != null) archivoElegido.onReceiveValue(null);
            archivoElegido = respuesta;
            try {
                startActivityForResult(params.createIntent(), ELEGIR_ARCHIVO);
                return true;
            } catch (RuntimeException sinSelector) {
                archivoElegido = null;
                Toast.makeText(MainActivity.this, "Este aparato no tiene selector de archivos.", Toast.LENGTH_LONG).show();
                return false;
            }
        }
    }

    @Override
    protected void onActivityResult(int peticion, int resultado, Intent datos) {
        IntentResult escaneo=IntentIntegrator.parseActivityResult(peticion,resultado,datos);
        if(escaneo!=null){if(escaneo.getContents()!=null)abrirVinculacion(escaneo.getContents());return;}

        if (peticion == REPRODUCIR) {
            if (resultado == ReproductorActivity.RESULTADO_FALLO && datos != null) {
                Toast.makeText(this, "Se abre el reproductor alternativo.", Toast.LENGTH_LONG).show();
                web.loadUrl(origen + "/ver/" + Uri.encode(datos.getStringExtra(ReproductorActivity.ITEM)) + "?web=1&file=" + Uri.encode(datos.getStringExtra(ReproductorActivity.ARCHIVO)));
            } else if (resultado == ReproductorActivity.RESULTADO_BLOQUEADO) {
                // El servidor ha bloqueado el perfil: la web enseña la pantalla de PIN.
                web.evaluateJavascript("window.dispatchEvent(new Event('velora:bloqueado'))", null);
            } else {
                // La pantalla de debajo sigue ahí, con el foco donde estaba: solo refresca el progreso.
                web.evaluateJavascript("window.dispatchEvent(new Event('velora:volver'))", null);
            }
            return;
        }
        if (peticion == ELEGIR_ARCHIVO && archivoElegido != null) {
            archivoElegido.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(resultado, datos));
            archivoElegido = null;
        }
    }

    private void pantallaCompleta(boolean activa) {
        getWindow().getDecorView().setSystemUiVisibility(activa
                ? View.SYSTEM_UI_FLAG_FULLSCREEN | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION | View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                  | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION | View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                : View.SYSTEM_UI_FLAG_VISIBLE);
    }

    private void sinConexion() {
        if (isFinishing()) return;
        new AlertDialog.Builder(this)
                .setTitle("No se puede conectar con Velora")
                .setMessage("Servidor: " + origen + "\nComprueba que este aparato está en la red de casa.")
                .setCancelable(false)
                .setPositiveButton("Reintentar (recomendado)", (d, w) -> web.loadUrl(origen + "/"))
                .setNeutralButton("Cambiar servidor", (d, w) -> cambiarServidor())
                .show();
    }

    private void cambiarServidor() {
        EditText campo = new EditText(this);
        campo.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        campo.setText(origen);
        new AlertDialog.Builder(this)
                .setTitle("Dirección del servidor")
                .setMessage(Servidor.AYUDA)
                .setView(campo)
                .setCancelable(false)
                .setPositiveButton("Guardar", (d, w) -> {
                    String nuevo = Servidor.normalizar(campo.getText().toString());
                    if (nuevo == null) {
                        Toast.makeText(this, Servidor.NO_VALIDA, Toast.LENGTH_LONG).show();
                        cambiarServidor();
                        return;
                    }
                    origen = nuevo;
                    Servidor.guardar(this, nuevo);
                    web.loadUrl(origen + "/");
                })
                .setNegativeButton("Volver", (d, w) -> sinConexion())
                .show();
    }

    // El motor de la web no entrega la tecla Menú a la página, y OK sobre un campo no abre el teclado: se avisa desde aquí.
    @Override
    public boolean dispatchKeyEvent(KeyEvent e) {
        if (esTv && videoCompleto == null) {
            int tecla = e.getKeyCode();
            if (tecla == KeyEvent.KEYCODE_MENU) {
                if (e.getAction() == KeyEvent.ACTION_UP) web.evaluateJavascript("window.__veloraMenu && window.__veloraMenu()", null);
                return true;
            }
            if (e.getAction() == KeyEvent.ACTION_UP && (tecla == KeyEvent.KEYCODE_DPAD_CENTER || tecla == KeyEvent.KEYCODE_ENTER || tecla == KeyEvent.KEYCODE_NUMPAD_ENTER)) {
                web.evaluateJavascript("window.__veloraTeclado && window.__veloraTeclado()", null);
            }
        }
        return super.dispatchKeyEvent(e);
    }

    /** Lo visitado en la web, como ruta con su consulta; lo que no es Velora queda como null. */
    private java.util.List<String> rutasVisitadas() {
        android.webkit.WebBackForwardList lista = web.copyBackForwardList();
        java.util.List<String> rutas = new java.util.ArrayList<>();
        for (int i = 0; i < lista.getSize(); i++) {
            Uri u = Uri.parse(lista.getItemAtIndex(i).getUrl());
            rutas.add(Servidor.mismoOrigen(origen, u) ? u.getPath() + (u.getQuery() == null ? "" : "?" + u.getQuery()) : null);
        }
        return rutas;
    }

    @Override
    public void onBackPressed() {
        if (videoCompleto != null) {
            if (salirVideo != null) salirVideo.onCustomViewHidden();
            return;
        }
        web.evaluateJavascript("window.__veloraAtras ? window.__veloraAtras() : false", resultado -> {
            if ("true".equals(resultado)) return;
            String actual = web.getUrl();
            boolean raizApp = actual == null || ReglasReproductor.pantallaRaiz(Uri.parse(actual).getPath());
            int pasos = raizApp ? 0 : ReglasReproductor.pasosAtras(rutasVisitadas(), web.copyBackForwardList().getCurrentIndex());
            if (pasos < 0) {
                web.goBackOrForward(pasos); // Como una app: a la pantalla anterior, nunca a un vídeo ya visto.
            } else if (!raizApp && ruta == null) {
                web.loadUrl(origen + "/"); // Nada válido detrás: Atrás lleva al inicio, y desde ahí ofrece salir.
            } else if (ruta != null) {
                finish(); // Se abrió para una ruta concreta: al salir de ella se cierra.
            } else if (System.currentTimeMillis() - atrasPulsado < 2500) {
                finish();
            } else {
                atrasPulsado = System.currentTimeMillis();
                Toast.makeText(this, "Pulsa otra vez para salir", Toast.LENGTH_SHORT).show();
            }
        });
    }

    @Override
    protected void onResume() {
        super.onResume();
        web.onResume();
        pantallaCompleta(esTv || reproductorCompleto || videoCompleto != null);
        actualizador.continuar();
        if (!origen.isEmpty()) actualizador.comprobar(origen);
    }

    @Override
    protected void onPause() {
        CookieManager.getInstance().flush(); // La sesión sobrevive aunque el sistema cierre la app.
        web.onPause();
        super.onPause();
    }

    @Override
    protected void onSaveInstanceState(Bundle estado) {
        super.onSaveInstanceState(estado);
        web.saveState(estado);
    }

    @Override
    protected void onDestroy() {
        if (web == null) { // Ha pasado directamente a la pantalla nativa.
            super.onDestroy();
            return;
        }
        actualizador.cerrar();
        raiz.removeAllViews();
        web.destroy();
        super.onDestroy();
    }

    private String leerAsset(String nombre) {
        try (InputStream in = getAssets().open(nombre)) {
            ByteArrayOutputStream out = new ByteArrayOutputStream();
            byte[] b = new byte[8192];
            int r;
            while ((r = in.read(b)) > 0) out.write(b, 0, r);
            return out.toString("UTF-8");
        } catch (IOException imposible) {
            throw new IllegalStateException(imposible);
        }
    }
}
