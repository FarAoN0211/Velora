package es.velora.cliente;

import org.json.JSONObject;
import org.json.JSONArray;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.graphics.Color;
import android.graphics.Rect;
import android.graphics.drawable.GradientDrawable;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.text.Editable;
import android.text.InputType;
import android.text.TextWatcher;
import android.view.FocusFinder;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputMethodManager;
import android.webkit.CookieManager;
import android.widget.AdapterView;
import android.widget.BaseAdapter;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.GridView;
import android.widget.HorizontalScrollView;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * La app de TV: pantallas propias de Android, no una página. Solo reciben el foco las carátulas y las acciones; Atrás recorre estas pantallas
 * y nunca vuelve a un vídeo que ya terminó. La web se usa únicamente para el panel de control y como respaldo (véase {@link GuardianTv}).
 */
public final class TvActivity extends Activity {
    private static final int REPRODUCIR = 1;
    private static final long PARA_DAR_POR_ESTABLE_MS = 15000;
    private static final long ESPERA_BUSQUEDA_MS = 350;
    /** La cabecera no puede pasar del 40 % de la altura: debajo tiene que verse ya la primera fila. */
    private static final float ALTURA_CABECERA = 0.36f;
    private static final long CAMBIO_CABECERA_MS = 10000;

    private enum Tipo { INICIO, BUSCAR, BIBLIOTECA, FICHA }

    /** Una pantalla y lo necesario para dejarla como estaba al volver con Atrás. */
    private static final class Pantalla {
        final Tipo tipo;
        final String id;
        final String titulo;
        String foco;
        int posicion;
        List<ReglasTv.Tarjeta> items;
        boolean sinMas;
        String orden = ReglasTv.ORDENES[0][0];
        boolean inverso;
        String genero = "";
        String estado = "";
        String consulta = "";

        Pantalla(Tipo tipo, String id, String titulo) {
            this.tipo = tipo;
            this.id = id;
            this.titulo = titulo;
        }
    }

    private interface Tarea<T> {
        T hacer() throws Exception;
    }

    private interface Hecho<T> {
        void con(T r);
    }

    private final Handler hilo = new Handler(Looper.getMainLooper());
    private final ExecutorService fondo = Executors.newFixedThreadPool(2);
    private final Imagenes imagenes = new Imagenes();
    private final Runnable darPorEstable = () -> GuardianTv.estable(this);
    private final Runnable buscarAhora = this::buscar;
    private final Runnable cambiarCabecera = this::siguienteDestacada;
    private final List<View> enLateral = new ArrayList<>();

    private Actualizador actualizador;
    private String origen;
    private String aparato;
    private ApiVelora api;
    private LinearLayout lateral;
    private LinearLayout listaLateral;
    private FrameLayout contenido;
    private ReglasTv.Historial<Pantalla> historial;
    private Pantalla inicio;
    private Pantalla seccionAbierta;
    private ReglasTv.Usuario usuario;
    private int generacion;
    private long atrasPulsado;
    private View ultimoFoco;
    private boolean enEntrada;
    private boolean formularioEntrada;
    private boolean sinPerfiles;
    // Pantalla actual.
    private GridView rejilla;
    private Rejilla adaptador;
    private Pantalla enRejilla;
    private boolean cargandoMas;
    private EditText campoBuscar;
    private TextView avisoBuscar;
    private List<ReglasTv.Tarjeta> destacadas;
    private int destacada;
    private ImageView imagenCabecera;
    private TextView tituloCabecera;
    private TextView sinopsisCabecera;
    private View botonesCabecera;
    private TextView botonReproducirCabecera;

    @Override
    protected void onCreate(Bundle guardado) {
        super.onCreate(guardado);
        GuardianTv.arranque(this);
        hilo.postDelayed(darPorEstable, PARA_DAR_POR_ESTABLE_MS);
        origen = Servidor.leer(this);
        aparato = aparato();
        actualizador = new Actualizador(this);
        construir();
        arrancar();
    }

    private String aparato() {
        android.content.SharedPreferences p = getSharedPreferences("velora", MODE_PRIVATE);
        String id = p.getString("aparato", null);
        if (id == null) {
            id = UUID.randomUUID().toString();
            p.edit().putString("aparato", id).apply();
        }
        return id;
    }

    private void construir() {
        LinearLayout raiz = new LinearLayout(this);
        raiz.setBackgroundColor(VistasTv.FONDO);
        lateral = new LinearLayout(this);
        lateral.setOrientation(LinearLayout.VERTICAL);
        lateral.setBackgroundColor(VistasTv.LATERAL);
        lateral.setPadding(dp(10), dp(14), dp(10), dp(10));
        TextView marca = VistasTv.titulo(this, "VELORA", 16);
        marca.setTextColor(VistasTv.ACENTO);
        marca.setPadding(dp(10), 0, 0, dp(12));
        lateral.addView(marca);
        ScrollView desplazar = new ScrollView(this);
        desplazar.setVerticalScrollBarEnabled(false);
        desplazar.setOverScrollMode(View.OVER_SCROLL_NEVER);
        listaLateral = new LinearLayout(this);
        listaLateral.setOrientation(LinearLayout.VERTICAL);
        desplazar.addView(listaLateral);
        lateral.addView(desplazar, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
        raiz.addView(lateral, new LinearLayout.LayoutParams(dp(VistasTv.ANCHO_LATERAL), ViewGroup.LayoutParams.MATCH_PARENT));
        contenido = new FrameLayout(this);
        raiz.addView(contenido, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.MATCH_PARENT, 1));
        setContentView(raiz);
        pantallaCompleta();
        lateral.setVisibility(View.GONE);
    }

    private void pantallaCompleta() {
        getWindow().getDecorView().setSystemUiVisibility(View.SYSTEM_UI_FLAG_FULLSCREEN | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION | View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION | View.SYSTEM_UI_FLAG_LAYOUT_STABLE);
    }

    private View enfocado() {
        return getWindow().getDecorView().findFocus();
    }

    private int dp(float v) {
        return VistasTv.dp(this, v);
    }

    private int anchoPantalla() {
        return getResources().getDisplayMetrics().widthPixels;
    }

    private int altoPantalla() {
        return getResources().getDisplayMetrics().heightPixels;
    }

    private void renovarSesion() {
        api = new ApiVelora(origen, CookieManager.getInstance().getCookie(origen), aparato);
        imagenes.sesion(origen, api.cabeceras());
    }

    /** Una respuesta que llega cuando ya se ha cambiado de pantalla no debe pintar nada. */
    private <T> void cargar(Tarea<T> tarea, Hecho<T> bien, Hecho<Exception> mal) {
        int g = generacion;
        fondo.execute(() -> {
            T r;
            try {
                r = tarea.hacer();
            } catch (Exception e) {
                hilo.post(() -> {
                    if (vigente(g)) mal.con(e);
                });
                return;
            }
            T resultado = r;
            hilo.post(() -> {
                if (vigente(g)) bien.con(resultado);
            });
        });
    }

    private boolean vigente(int g) {
        return g == generacion && !isFinishing() && !isDestroyed();
    }

    private void arrancar() {
        generacion++;
        renovarSesion();
        lateral.setVisibility(View.GONE);
        enEntrada = false;
        cargando();
        ApiVelora a = api;
        cargar(() -> {
            if (ReglasTv.requiereConfigurar(a.get("/api/v1/system/info"))) return null;
            ReglasTv.Usuario u = ReglasTv.leerUsuario(a.get("/api/v1/me"));
            return new Object[] {u, ReglasTv.leerBibliotecas(a.get("/api/v1/libraries"))};
        }, r -> {
            if (r == null) {
                // El asistente de primer arranque es administración: se queda en la web.
                abrirWeb("/");
                return;
            }
            usuario = (ReglasTv.Usuario) r[0];
            @SuppressWarnings("unchecked")
            List<ReglasTv.Biblioteca> libs = (List<ReglasTv.Biblioteca>) r[1];
            pintarLateral(libs);
            lateral.setVisibility(View.VISIBLE);
            mostrar(inicio);
        }, this::fallo);
    }

    private void fallo(Exception e) {
        fallo(e, this::arrancar);
    }

    private void fallo(Exception e, Runnable reintentar) {
        if (e instanceof ApiVelora.ErrorApi && ((ApiVelora.ErrorApi) e).estado == 401) {
            entrada();
            return;
        }
        boolean sinRed = !(e instanceof ApiVelora.ErrorApi);
        vaciar();
        LinearLayout c = centrado();
        TextView t = VistasTv.titulo(this, sinRed ? "No se puede conectar con Velora" : "No se ha podido cargar", 18);
        t.setGravity(Gravity.CENTER);
        c.addView(t);
        TextView d = VistasTv.texto(this, sinRed ? "Servidor: " + origen + "\nComprueba que este aparato está en la red de casa." : "Vuelve a intentarlo.", 13, VistasTv.APAGADO);
        d.setGravity(Gravity.CENTER);
        d.setPadding(0, dp(8), 0, dp(14));
        c.addView(d);
        LinearLayout botones = new LinearLayout(this);
        View uno = accion(botones, "Reintentar", v -> reintentar.run());
        if (sinRed) accion(botones, "Cambiar servidor", v -> cambiarServidor());
        c.addView(botones);
        uno.requestFocus();
    }

    private void cambiarServidor() {
        EditText campo = new EditText(this);
        campo.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        campo.setText(origen);
        new AlertDialog.Builder(this)
                .setTitle("Dirección del servidor")
                .setMessage(Servidor.AYUDA)
                .setView(campo)
                .setPositiveButton("Guardar", (d, w) -> {
                    String nuevo = Servidor.normalizar(campo.getText().toString());
                    if (nuevo == null) {
                        Toast.makeText(this, Servidor.NO_VALIDA, Toast.LENGTH_LONG).show();
                        return;
                    }
                    origen = nuevo;
                    Servidor.guardar(this, nuevo);
                    arrancar();
                })
                .setNegativeButton("Volver", null)
                .show();
    }

    private void cargando() {
        vaciar();
        LinearLayout c = centrado();
        c.addView(VistasTv.texto(this, "Cargando…", 14, VistasTv.APAGADO));
    }

    private void vaciar() {
        hilo.removeCallbacks(buscarAhora);
        hilo.removeCallbacks(cambiarCabecera);
        contenido.removeAllViews();
        rejilla = null;
        adaptador = null;
        enRejilla = null;
        campoBuscar = null;
        avisoBuscar = null;
        ultimoFoco = null;
        imagenCabecera = null;
    }

    private LinearLayout centrado() {
        LinearLayout c = new LinearLayout(this);
        c.setOrientation(LinearLayout.VERTICAL);
        c.setGravity(Gravity.CENTER);
        c.setPadding(dp(24), dp(16), dp(24), dp(16));
        contenido.addView(c, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        return c;
    }

    private TextView accion(LinearLayout fila, String texto, View.OnClickListener alPulsar) {
        TextView b = VistasTv.boton(this, texto);
        b.setOnClickListener(alPulsar);
        LinearLayout.LayoutParams l = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        l.setMarginEnd(dp(8));
        fila.addView(b, l);
        return b;
    }

    // ------- Lateral -------

    private void pintarLateral(List<ReglasTv.Biblioteca> libs) {
        listaLateral.removeAllViews();
        enLateral.clear();
        inicio = new Pantalla(Tipo.INICIO, null, "Inicio");
        historial = new ReglasTv.Historial<>(inicio);
        entradaLateral("Inicio", R.drawable.ic_inicio, inicio);
        // Buscar va antes de las bibliotecas, y es el único buscador de la app.
        entradaLateral("Buscar", R.drawable.ic_buscar, new Pantalla(Tipo.BUSCAR, null, "Buscar"));
        for (ReglasTv.Biblioteca b : libs) {
            entradaLateral(b.nombre, "tv".equals(b.tipo) ? R.drawable.ic_serie : R.drawable.ic_pelicula, new Pantalla(Tipo.BIBLIOTECA, b.id, b.nombre));
        }
        if (usuario.admin) {
            View panel = VistasTv.seccion(this, "Panel de control", R.drawable.ic_ajustes);
            panel.setOnClickListener(v -> abrirWeb("/admin"));
            anadirLateral(panel);
        }
        View salir = VistasTv.seccion(this, "Cerrar sesión", R.drawable.ic_salir);
        salir.setOnClickListener(v -> confirmarSalida());
        anadirLateral(salir);
    }

    private void entradaLateral(String nombre, int icono, Pantalla p) {
        View v = VistasTv.seccion(this, nombre, icono);
        v.setOnClickListener(x -> irASeccion(p));
        v.setTag(R.id.seccion_pantalla, p);
        enLateral.add(v);
        anadirLateral(v);
    }

    private void anadirLateral(View v) {
        LinearLayout.LayoutParams l = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        l.bottomMargin = dp(2);
        listaLateral.addView(v, l);
    }

    private void irASeccion(Pantalla p) {
        guardarFoco();
        // Volver a la sección que ya está abierta no la recarga: solo entra en ella.
        if (historial.actual() == p) {
            entrarEnContenido();
            return;
        }
        historial.seccion(p);
        mostrar(p);
    }

    /** Dentro de una ficha sigue marcada la sección de la que se viene. */
    private void marcarSeccion(Pantalla p) {
        if (p.tipo != Tipo.FICHA) seccionAbierta = p;
        for (View v : enLateral) v.setActivated(v.getTag(R.id.seccion_pantalla) == seccionAbierta);
    }

    private void confirmarSalida() {
        new AlertDialog.Builder(this)
                .setTitle("Cerrar sesión")
                .setMessage("Tendrás que volver a entrar en este aparato.")
                .setPositiveButton("Cerrar sesión", (d, w) -> {
                    ApiVelora a = api;
                    cargar(() -> a.enviar("POST", "/api/v1/auth/logout", null), r -> {
                        CookieManager.getInstance().removeAllCookies(null);
                        CookieManager.getInstance().flush();
                        arrancar();
                    }, e -> Toast.makeText(this, "No se ha podido cerrar la sesión.", Toast.LENGTH_LONG).show());
                })
                .setNegativeButton("Seguir aquí", null)
                .show();
    }

    // ------- Navegación entre pantallas -------

    private void mostrar(Pantalla p) {
        generacion++;
        marcarSeccion(p);
        cargando();
        switch (p.tipo) {
            case INICIO:
                pedirInicio(p);
                break;
            case BUSCAR:
                pintarBuscar(p);
                break;
            case BIBLIOTECA:
                pintarBiblioteca(p);
                break;
            case FICHA:
                pedirFicha(p);
                break;
        }
    }

    private void abrirFicha(ReglasTv.Tarjeta t) {
        guardarFoco();
        String web = t.fichaWeb();
        if (web != null) { // series: la ficha web (S06)
            abrirWeb(web);
            return;
        }
        Pantalla p = new Pantalla(Tipo.FICHA, t.id, t.titulo);
        historial.abrir(p);
        mostrar(p);
    }

    /** Lo enfocado se recuerda por su etiqueta, para recuperarlo aunque la pantalla se haya vuelto a construir. */
    private void guardarFoco() {
        Pantalla p = historial == null ? null : historial.actual();
        if (p == null) return;
        if (enRejilla == p && rejilla != null && rejilla.getSelectedItemPosition() >= 0) p.posicion = rejilla.getSelectedItemPosition();
        View f = enfocado();
        if (f == null || !dentro(contenido, f)) return;
        p.foco = f.getTag() instanceof String ? (String) f.getTag() : null;
    }

    private void enfocar(String etiqueta, View porDefecto) {
        View f = etiqueta == null ? null : contenido.findViewWithTag(etiqueta);
        if (f == null) f = porDefecto;
        if (f != null) f.requestFocus();
    }

    private static boolean dentro(ViewGroup padre, View v) {
        for (View x = v; x != null; x = x.getParent() instanceof View ? (View) x.getParent() : null) {
            if (x == padre) return true;
        }
        return false;
    }

    @Override
    public void onBackPressed() {
        if (enEntrada) {
            if (formularioEntrada && !sinPerfiles) {
                entrada();
                return;
            }
        } else if (historial != null && lateral.getVisibility() == View.VISIBLE) {
            Pantalla anterior = historial.atras();
            if (anterior != null) {
                mostrar(anterior);
                return;
            }
        }
        if (System.currentTimeMillis() - atrasPulsado < 2500) {
            finish();
        } else {
            atrasPulsado = System.currentTimeMillis();
            Toast.makeText(this, "Pulsa otra vez para salir", Toast.LENGTH_SHORT).show();
        }
    }

    // El orden del foco se decide aquí: dejarlo a la búsqueda del sistema lo haría saltar de una fila a otra, o del contenido al lateral al subir y bajar.
    @Override
    public boolean dispatchKeyEvent(KeyEvent e) {
        int tecla = e.getKeyCode();
        if (tecla == KeyEvent.KEYCODE_MENU) {
            if (e.getAction() == KeyEvent.ACTION_UP) menuDelFoco();
            return true;
        }
        if (e.getAction() == KeyEvent.ACTION_DOWN && moverFoco(tecla)) return true;
        return super.dispatchKeyEvent(e);
    }

    private boolean moverFoco(int tecla) {
        int dir = sentido(tecla);
        View f = enfocado();
        if (dir == 0 || f == null || lateral.getVisibility() != View.VISIBLE) return false;
        if (dentro(lateral, f)) {
            if (dir == View.FOCUS_RIGHT) {
                entrarEnContenido();
            } else if (dir != View.FOCUS_LEFT) {
                saltar(FocusFinder.getInstance().findNextFocus(listaLateral, f, dir), dir);
            }
            return true;
        }
        if (!dentro(contenido, f)) return false;
        if (f == rejilla) return moverEnRejilla(dir);
        if (f instanceof EditText && (dir == View.FOCUS_LEFT || dir == View.FOCUS_RIGHT)) {
            EditText campo = (EditText) f;
            boolean alBorde = dir == View.FOCUS_LEFT ? campo.getSelectionStart() <= 0 : campo.getSelectionEnd() >= campo.length();
            if (!alBorde) return false;
        }
        View siguiente = FocusFinder.getInstance().findNextFocus(contenido, f, dir);
        boolean mismaFila = siguiente != null && siguiente.getParent() == f.getParent();
        if (dir == View.FOCUS_LEFT && !mismaFila) {
            irAlLateral(f);
        } else if (dir == View.FOCUS_LEFT || dir == View.FOCUS_RIGHT) {
            if (mismaFila) saltar(siguiente, dir);
        } else {
            saltar(siguiente, dir);
        }
        return true;
    }

    private static int sentido(int tecla) {
        switch (tecla) {
            case KeyEvent.KEYCODE_DPAD_LEFT: return View.FOCUS_LEFT;
            case KeyEvent.KEYCODE_DPAD_RIGHT: return View.FOCUS_RIGHT;
            case KeyEvent.KEYCODE_DPAD_UP: return View.FOCUS_UP;
            case KeyEvent.KEYCODE_DPAD_DOWN: return View.FOCUS_DOWN;
            default: return 0;
        }
    }

    private static void saltar(View destino, int dir) {
        if (destino != null) destino.requestFocus(dir);
    }

    private boolean moverEnRejilla(int dir) {
        int pos = rejilla.getSelectedItemPosition();
        int columnas = rejilla.getNumColumns();
        int total = adaptador.getCount();
        if (pos < 0) return false;
        switch (dir) {
            case View.FOCUS_LEFT:
                if (pos % columnas != 0) return false;
                irAlLateral(rejilla);
                return true;
            case View.FOCUS_RIGHT:
                return pos % columnas == columnas - 1 || pos == total - 1;
            case View.FOCUS_DOWN:
                return pos + columnas >= total && (pos / columnas) == ((total - 1) / columnas);
            default:
                if (pos >= columnas) return false;
                saltar(FocusFinder.getInstance().findNextFocus(contenido, rejilla, View.FOCUS_UP), View.FOCUS_UP);
                return true;
        }
    }

    private void irAlLateral(View desde) {
        ultimoFoco = desde;
        for (View v : enLateral) {
            if (v.isActivated()) {
                v.requestFocus();
                return;
            }
        }
        if (!enLateral.isEmpty()) enLateral.get(0).requestFocus();
    }

    private void entrarEnContenido() {
        if (ultimoFoco != null && ultimoFoco.isAttachedToWindow() && ultimoFoco.requestFocus()) return;
        ArrayList<View> posibles = new ArrayList<>();
        contenido.addFocusables(posibles, View.FOCUS_RIGHT);
        if (!posibles.isEmpty()) posibles.get(0).requestFocus();
    }

    private void menuDelFoco() {
        View f = enfocado();
        if (f == rejilla && adaptador != null && rejilla.getSelectedItemPosition() >= 0) {
            menuDe(adaptador.items.get(rejilla.getSelectedItemPosition()), false);
        } else if (f != null && f.getTag(R.id.carta_tarjeta) instanceof ReglasTv.Tarjeta) {
            menuDe((ReglasTv.Tarjeta) f.getTag(R.id.carta_tarjeta), String.valueOf(f.getTag()).startsWith("continuar:"));
        }
    }

    /** El menú de la carátula, con OK mantenido o con la tecla Menú. */
    private void menuDe(ReglasTv.Tarjeta t, boolean enContinuar) {
        List<String> nombres = new ArrayList<>();
        List<Runnable> acciones = new ArrayList<>();
        nombres.add(t.reanudable() ? "Continuar" : "Reproducir");
        acciones.add(() -> {
            if (t.esSerie()) abrirFicha(t);
            else reproducir(t.id, "", -1, false);
        });
        if (t.reanudable()) {
            nombres.add("Desde el principio");
            acciones.add(() -> reproducir(t.id, "", -1, true));
        }
        if (!t.trailer.isEmpty()) {
            nombres.add("Ver tráiler");
            acciones.add(() -> reproducir(t.id, t.trailer, -1, false));
        }
        nombres.add(t.vista ? "Marcar como no vista" : "Marcar como vista");
        acciones.add(() -> cambiarVista(t.id, !t.vista));
        if (enContinuar) {
            nombres.add("Quitar de «Continuar viendo»");
            acciones.add(() -> ocultarContinuar(t.id));
        }
        nombres.add("Ir a la ficha");
        acciones.add(() -> abrirFicha(t));
        new AlertDialog.Builder(this)
                .setTitle(t.titulo)
                .setItems(nombres.toArray(new String[0]), (d, w) -> acciones.get(w).run())
                .show();
    }

    private void recargar() {
        guardarFoco();
        mostrar(historial.actual());
    }

    /** En una rejilla larga se actualiza la carátula sin perder el sitio; con el filtro de vistas, puede tener que desaparecer. */
    private void trasMarcar(String id, boolean vista) {
        Pantalla p = historial.actual();
        if (adaptador == null || !p.estado.isEmpty()) {
            p.items = null;
            recargar();
            return;
        }
        for (ReglasTv.Tarjeta t : adaptador.items) {
            if (!t.id.equals(id)) continue;
            t.vista = vista;
            if (vista) t.posicionMs = 0;
        }
        adaptador.notifyDataSetChanged();
    }

    private void cambiarVista(String id, boolean vista) {
        ApiVelora a = api;
        cargar(() -> a.enviar(vista ? "POST" : "DELETE", "/api/v1/me/items/" + ReglasTv.cifrar(id) + "/played", null), r -> trasMarcar(id, vista), this::avisarFallo);
    }

    private void ocultarContinuar(String id) {
        ApiVelora a = api;
        cargar(() -> a.enviar("POST", "/api/v1/me/items/" + ReglasTv.cifrar(id) + "/continue-hidden", null), r -> recargar(), this::avisarFallo);
    }

    private void avisarFallo(Exception e) {
        if (e instanceof ApiVelora.ErrorApi && ((ApiVelora.ErrorApi) e).estado == 401) {
            entrada();
            return;
        }
        boolean sinAcceso = e instanceof ApiVelora.ErrorApi && ((ApiVelora.ErrorApi) e).estado == 404;
        Toast.makeText(this, sinAcceso ? "Ya no tienes acceso a este título." : "No se ha podido completar.", Toast.LENGTH_LONG).show();
    }

    // ------- Inicio -------

    private void pedirInicio(Pantalla p) {
        ApiVelora a = api;
        cargar(() -> ReglasTv.leerInicio(a.get("/api/v1/home")), r -> pintarInicio(p, r), e -> fallo(e, () -> mostrar(p)));
    }

    private void pintarInicio(Pantalla p, ReglasTv.Inicio datos) {
        vaciar();
        ScrollView desplazar = new ScrollView(this);
        desplazar.setVerticalScrollBarEnabled(false);
        desplazar.setOverScrollMode(View.OVER_SCROLL_NEVER);
        LinearLayout columna = new LinearLayout(this);
        columna.setOrientation(LinearLayout.VERTICAL);
        columna.setPadding(0, 0, 0, dp(16));
        desplazar.addView(columna);
        contenido.addView(desplazar);
        View primero = null;
        if (!datos.destacadas.isEmpty()) {
            columna.addView(cabecera(datos.destacadas), new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, Math.round(altoPantalla() * ALTURA_CABECERA)));
            primero = botonReproducirCabecera;
        }
        if (!datos.continuar.isEmpty()) {
            View v = fila(columna, "Continuar viendo", datos.continuar, true, "continuar");
            if (primero == null) primero = v;
        }
        for (int i = 0; i < datos.filas.size(); i++) {
            ReglasTv.Fila f = datos.filas.get(i);
            View v = fila(columna, "Novedades en " + f.titulo, f.items, false, "fila" + i);
            if (primero == null) primero = v;
        }
        if (primero == null) {
            contenido.removeAllViews();
            centrado().addView(VistasTv.texto(this, "Todavía no hay nada que ver aquí.", 14, VistasTv.APAGADO));
            return;
        }
        enfocar(p.foco, primero);
    }

    private View cabecera(List<ReglasTv.Tarjeta> lista) {
        destacadas = lista;
        destacada = 0;
        FrameLayout c = new FrameLayout(this);
        imagenCabecera = new ImageView(this);
        imagenCabecera.setScaleType(ImageView.ScaleType.CENTER_CROP);
        c.addView(imagenCabecera, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        View velo = new View(this);
        velo.setBackground(VistasTv.degradado(GradientDrawable.Orientation.LEFT_RIGHT, Color.argb(235, 14, 17, 22), Color.argb(40, 14, 17, 22)));
        c.addView(velo, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        LinearLayout t = new LinearLayout(this);
        t.setOrientation(LinearLayout.VERTICAL);
        t.setGravity(Gravity.BOTTOM);
        t.setPadding(dp(20), dp(10), dp(20), dp(12));
        tituloCabecera = VistasTv.titulo(this, "", 20);
        tituloCabecera.setSingleLine(true);
        t.addView(tituloCabecera);
        sinopsisCabecera = VistasTv.texto(this, "", 12, VistasTv.APAGADO);
        sinopsisCabecera.setMaxLines(2);
        sinopsisCabecera.setEllipsize(android.text.TextUtils.TruncateAt.END);
        sinopsisCabecera.setPadding(0, dp(4), 0, dp(8));
        t.addView(sinopsisCabecera, new LinearLayout.LayoutParams(Math.round((anchoPantalla() - dp(VistasTv.ANCHO_LATERAL)) * 0.62f), ViewGroup.LayoutParams.WRAP_CONTENT));
        LinearLayout botones = new LinearLayout(this);
        botonReproducirCabecera = accion(botones, "Reproducir", v -> {
            ReglasTv.Tarjeta d = destacadas.get(destacada);
            if (d.esSerie()) abrirFicha(d);
            else reproducir(d.id, "", -1, false);
        });
        botonReproducirCabecera.setTag("cabecera-reproducir");
        TextView info = accion(botones, "Más información", v -> abrirFicha(destacadas.get(destacada)));
        info.setTag("cabecera-info");
        botonesCabecera = botones;
        t.addView(botones);
        c.addView(t, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        pintarDestacada();
        if (lista.size() > 1) hilo.postDelayed(cambiarCabecera, CAMBIO_CABECERA_MS);
        return c;
    }

    private void pintarDestacada() {
        ReglasTv.Tarjeta d = destacadas.get(destacada);
        tituloCabecera.setText(d.titulo);
        sinopsisCabecera.setText(d.sinopsis);
        botonReproducirCabecera.setText(d.reanudable() ? "Continuar" : "Reproducir");
        imagenes.poner(imagenCabecera, d.imagenAncha(), anchoPantalla(), Math.round(altoPantalla() * ALTURA_CABECERA));
    }

    private void siguienteDestacada() {
        if (imagenCabecera == null) return;
        // No se cambia lo que el usuario está a punto de pulsar.
        View f = enfocado();
        if (f == null || !dentro((ViewGroup) botonesCabecera, f)) {
            destacada = (destacada + 1) % destacadas.size();
            pintarDestacada();
        }
        hilo.postDelayed(cambiarCabecera, CAMBIO_CABECERA_MS);
    }

    private View fila(LinearLayout columna, String titulo, List<ReglasTv.Tarjeta> items, boolean panoramica, String nombre) {
        LinearLayout bloque = new LinearLayout(this);
        bloque.setOrientation(LinearLayout.VERTICAL);
        bloque.setClipChildren(false);
        bloque.setPadding(dp(20), dp(10), 0, 0);
        TextView t = VistasTv.titulo(this, titulo, 14);
        t.setPadding(0, 0, 0, dp(6));
        bloque.addView(t);
        HorizontalScrollView h = new HorizontalScrollView(this);
        h.setHorizontalScrollBarEnabled(false);
        h.setOverScrollMode(View.OVER_SCROLL_NEVER);
        h.setClipChildren(false);
        h.setClipToPadding(false);
        h.setPadding(0, dp(4), 0, dp(4));
        LinearLayout tira = new LinearLayout(this);
        tira.setClipChildren(false);
        tira.setPadding(dp(4), 0, dp(20), 0);
        View primera = null;
        for (ReglasTv.Tarjeta item : items) {
            VistasTv.Carta carta = new VistasTv.Carta(this, panoramica);
            carta.pintar(item, imagenes, panoramica);
            VistasTv.recibeFoco(carta);
            carta.setTag(nombre + ":" + item.id);
            carta.setTag(R.id.carta_tarjeta, item);
            carta.setOnClickListener(v -> abrirFicha(item));
            carta.setOnLongClickListener(v -> {
                menuDe(item, panoramica);
                return true;
            });
            carta.setOnFocusChangeListener((v, si) -> {
                carta.resaltar(si);
                // Que el título de la fila entre también en pantalla, no solo la carátula.
                if (si) bloque.requestRectangleOnScreen(new Rect(0, 0, bloque.getWidth(), bloque.getHeight()));
            });
            LinearLayout.LayoutParams l = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
            l.setMarginEnd(dp(VistasTv.HUECO));
            tira.addView(carta, l);
            if (primera == null) primera = carta;
        }
        h.addView(tira);
        bloque.addView(h);
        columna.addView(bloque);
        return primera;
    }

    // ------- Rejilla de carátulas, en la biblioteca y en Buscar -------

    private final class Rejilla extends BaseAdapter {
        final List<ReglasTv.Tarjeta> items;

        Rejilla(List<ReglasTv.Tarjeta> items) {
            this.items = items;
        }

        @Override
        public int getCount() {
            return items.size();
        }

        @Override
        public Object getItem(int p) {
            return items.get(p);
        }

        @Override
        public long getItemId(int p) {
            return p;
        }

        @Override
        public View getView(int p, View reciclada, ViewGroup padre) {
            VistasTv.Carta c = reciclada instanceof VistasTv.Carta ? (VistasTv.Carta) reciclada : new VistasTv.Carta(TvActivity.this, false);
            c.pintar(items.get(p), imagenes, false);
            c.resaltar(padre.hasFocus() && ((GridView) padre).getSelectedItemPosition() == p);
            if (p >= items.size() - ReglasTv.PAGINA / 3) cargarMas();
            return c;
        }
    }

    private GridView nuevaRejilla(List<ReglasTv.Tarjeta> items) {
        adaptador = new Rejilla(items);
        GridView g = new GridView(this);
        int celda = dp(VistasTv.ANCHO_CARATULA);
        int hueco = dp(VistasTv.HUECO);
        int disponible = anchoPantalla() - dp(VistasTv.ANCHO_LATERAL) - dp(20) * 2;
        g.setNumColumns(Math.max(1, (disponible + hueco) / (celda + hueco)));
        g.setColumnWidth(celda);
        g.setStretchMode(GridView.NO_STRETCH);
        g.setHorizontalSpacing(hueco);
        g.setVerticalSpacing(dp(10));
        g.setSelector(VistasTv.transparente());
        g.setVerticalScrollBarEnabled(false);
        g.setOverScrollMode(View.OVER_SCROLL_NEVER);
        g.setClipChildren(false);
        g.setClipToPadding(false);
        g.setPadding(dp(20), dp(6), dp(20), dp(16));
        g.setFocusable(!items.isEmpty());
        g.setFocusableInTouchMode(!items.isEmpty());
        g.setAdapter(adaptador);
        g.setOnItemClickListener((a, v, p, i) -> abrirFicha(adaptador.items.get(p)));
        g.setOnItemLongClickListener((a, v, p, i) -> {
            menuDe(adaptador.items.get(p), false);
            return true;
        });
        g.setOnItemSelectedListener(new AdapterView.OnItemSelectedListener() {
            private View anterior;

            @Override
            public void onItemSelected(AdapterView<?> a, View v, int p, long i) {
                resaltarCelda(anterior, false);
                anterior = v;
                resaltarCelda(v, g.hasFocus());
            }

            @Override
            public void onNothingSelected(AdapterView<?> a) {
                resaltarCelda(anterior, false);
                anterior = null;
            }
        });
        g.setOnFocusChangeListener((v, si) -> resaltarCelda(g.getSelectedView(), si));
        rejilla = g;
        return g;
    }

    private static void resaltarCelda(View v, boolean si) {
        if (v instanceof VistasTv.Carta) ((VistasTv.Carta) v).resaltar(si);
    }

    private void cargarMas() {
        Pantalla p = enRejilla;
        if (p == null || p.sinMas || cargandoMas || p.tipo != Tipo.BIBLIOTECA) return;
        cargandoMas = true;
        ApiVelora a = api;
        int desde = p.items.size();
        cargar(() -> ReglasTv.leerItems(a.get(ReglasTv.rutaBiblioteca(p.id, desde, p.orden, p.inverso, p.genero, p.estado))), r -> {
            cargandoMas = false;
            if (r.size() < ReglasTv.PAGINA) p.sinMas = true;
            p.items.addAll(r);
            if (adaptador != null) adaptador.notifyDataSetChanged();
        }, e -> {
            // Se deja de insistir hasta volver a abrir la biblioteca: sin esto se reintentaría a cada carátula pintada.
            cargandoMas = false;
            p.sinMas = true;
        });
    }

    // ------- Biblioteca -------

    private void pintarBiblioteca(Pantalla p) {
        if (p.items != null) {
            componerBiblioteca(p);
            return;
        }
        ApiVelora a = api;
        cargar(() -> ReglasTv.leerItems(a.get(ReglasTv.rutaBiblioteca(p.id, 0, p.orden, p.inverso, p.genero, p.estado))), r -> {
            p.items = new ArrayList<>(r);
            p.sinMas = r.size() < ReglasTv.PAGINA;
            componerBiblioteca(p);
        }, e -> fallo(e, () -> mostrar(p)));
    }

    /** Sin buscador: buscar solo se hace desde el lateral. */
    private void componerBiblioteca(Pantalla p) {
        vaciar();
        cargandoMas = false;
        LinearLayout c = new LinearLayout(this);
        c.setOrientation(LinearLayout.VERTICAL);
        LinearLayout barra = new LinearLayout(this);
        barra.setGravity(Gravity.CENTER_VERTICAL);
        barra.setPadding(dp(20), dp(12), dp(12), dp(4));
        TextView t = VistasTv.titulo(this, p.titulo, 18);
        t.setSingleLine(true);
        t.setEllipsize(android.text.TextUtils.TruncateAt.END);
        barra.addView(t, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        filtro(barra, "filtro-orden", nombreOrden(p), v -> elegirOrden(p));
        filtro(barra, "filtro-sentido", nombreSentido(p), v -> {
            p.inverso = !p.inverso;
            reiniciarBiblioteca(p);
        });
        filtro(barra, "filtro-genero", p.genero.isEmpty() ? "Género" : p.genero, v -> elegirGenero(p));
        filtro(barra, "filtro-estado", nombreEstado(p), v -> elegirEstado(p));
        c.addView(barra);
        if (p.items.isEmpty()) {
            TextView vacio = VistasTv.texto(this, "No hay títulos que coincidan.", 14, VistasTv.APAGADO);
            vacio.setGravity(Gravity.CENTER);
            c.addView(vacio, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
            contenido.addView(c);
            enfocar(p.foco, barra.getChildAt(1));
            return;
        }
        GridView g = nuevaRejilla(p.items);
        enRejilla = p;
        c.addView(g, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
        contenido.addView(c);
        if (p.posicion >= p.items.size()) p.posicion = 0;
        g.setSelection(p.posicion);
        if (p.foco != null && p.foco.startsWith("filtro-")) enfocar(p.foco, g);
        else g.requestFocus();
    }

    private void filtro(LinearLayout barra, String etiqueta, String texto, View.OnClickListener alPulsar) {
        TextView b = accion(barra, texto, alPulsar);
        b.setTag(etiqueta);
        b.setTextSize(12);
    }

    private void reiniciarBiblioteca(Pantalla p) {
        guardarFoco();
        p.items = null;
        p.posicion = 0;
        mostrar(p);
    }

    private static String nombreOrden(Pantalla p) {
        for (String[] o : ReglasTv.ORDENES) if (o[0].equals(p.orden)) return o[1];
        return ReglasTv.ORDENES[0][1];
    }

    private static String nombreSentido(Pantalla p) {
        for (String[] o : ReglasTv.ORDENES) if (o[0].equals(p.orden)) return p.inverso ? o[3] : o[2];
        return "";
    }

    private static String nombreEstado(Pantalla p) {
        for (String[] e : ReglasTv.ESTADOS) if (e[0].equals(p.estado)) return e[1];
        return ReglasTv.ESTADOS[0][1];
    }

    private void elegirOrden(Pantalla p) {
        String[] nombres = new String[ReglasTv.ORDENES.length];
        for (int i = 0; i < nombres.length; i++) nombres[i] = ReglasTv.ORDENES[i][1];
        new AlertDialog.Builder(this).setTitle("Ordenar por").setItems(nombres, (d, w) -> {
            p.orden = ReglasTv.ORDENES[w][0];
            reiniciarBiblioteca(p);
        }).show();
    }

    private void elegirEstado(Pantalla p) {
        String[] nombres = new String[ReglasTv.ESTADOS.length];
        for (int i = 0; i < nombres.length; i++) nombres[i] = ReglasTv.ESTADOS[i][1];
        new AlertDialog.Builder(this).setTitle("Mostrar").setItems(nombres, (d, w) -> {
            p.estado = ReglasTv.ESTADOS[w][0];
            reiniciarBiblioteca(p);
        }).show();
    }

    private void elegirGenero(Pantalla p) {
        ApiVelora a = api;
        cargar(() -> ReglasTv.leerGeneros(a.get("/api/v1/genres?library_id=" + ReglasTv.cifrar(p.id))), generos -> {
            String[] nombres = new String[generos.size() + 1];
            nombres[0] = "Todos los géneros";
            for (int i = 0; i < generos.size(); i++) nombres[i + 1] = generos.get(i);
            new AlertDialog.Builder(this).setTitle("Género").setItems(nombres, (d, w) -> {
                p.genero = w == 0 ? "" : nombres[w];
                reiniciarBiblioteca(p);
            }).show();
        }, this::avisarFallo);
    }

    // ------- Buscar -------

    private void pintarBuscar(Pantalla p) {
        vaciar();
        LinearLayout c = new LinearLayout(this);
        c.setOrientation(LinearLayout.VERTICAL);
        c.setPadding(0, dp(14), 0, 0);
        campoBuscar = new EditText(this);
        campoBuscar.setHint("Título de la película");
        campoBuscar.setHintTextColor(VistasTv.APAGADO);
        campoBuscar.setTextColor(VistasTv.TEXTO);
        campoBuscar.setTextSize(14);
        campoBuscar.setSingleLine(true);
        campoBuscar.setInputType(InputType.TYPE_CLASS_TEXT);
        campoBuscar.setImeOptions(EditorInfo.IME_ACTION_SEARCH);
        campoBuscar.setTag("buscar-campo");
        campoBuscar.setBackground(fondoCampo());
        campoBuscar.setPadding(dp(12), dp(8), dp(12), dp(8));
        campoBuscar.setText(p.consulta);
        campoBuscar.setSelection(p.consulta.length());
        campoBuscar.addTextChangedListener(new TextWatcher() {
            @Override
            public void beforeTextChanged(CharSequence s, int a, int b, int d) {}

            @Override
            public void onTextChanged(CharSequence s, int a, int b, int d) {}

            @Override
            public void afterTextChanged(Editable s) {
                p.consulta = s.toString();
                p.items = null;
                p.posicion = 0;
                hilo.removeCallbacks(buscarAhora);
                hilo.postDelayed(buscarAhora, ESPERA_BUSQUEDA_MS);
            }
        });
        campoBuscar.setOnEditorActionListener((v, accion, ev) -> {
            ocultarTeclado();
            hilo.removeCallbacks(buscarAhora);
            buscar();
            return true;
        });
        LinearLayout.LayoutParams lc = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lc.setMargins(dp(20), 0, dp(20), dp(8));
        c.addView(campoBuscar, lc);
        avisoBuscar = VistasTv.texto(this, "", 13, VistasTv.APAGADO);
        avisoBuscar.setPadding(dp(20), dp(4), dp(20), dp(4));
        c.addView(avisoBuscar);
        FrameLayout resultados = new FrameLayout(this);
        resultados.setTag("buscar-resultados");
        c.addView(resultados, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
        contenido.addView(c);
        if (p.items != null) {
            ponerResultados(p);
            if (p.items.isEmpty()) campoBuscar.requestFocus();
            else rejilla.requestFocus();
            return;
        }
        campoBuscar.requestFocus();
        avisoBuscar.setText("Escribe para buscar en tus bibliotecas.");
        if (!p.consulta.trim().isEmpty()) buscar();
    }

    private GradientDrawable fondoCampo() {
        GradientDrawable d = new GradientDrawable();
        d.setCornerRadius(dp(6));
        d.setColor(VistasTv.ELEVADO);
        d.setStroke(dp(2), VistasTv.ACENTO);
        return d;
    }

    private void buscar() {
        if (campoBuscar == null || historial == null) return;
        Pantalla p = historial.actual();
        String consulta = p.consulta.trim();
        if (consulta.isEmpty()) {
            p.items = null;
            avisoBuscar.setText("Escribe para buscar en tus bibliotecas.");
            quitarResultados();
            return;
        }
        ApiVelora a = api;
        // Una consulta más nueva descarta la respuesta de la anterior, que puede llegar después.
        cargar(() -> ReglasTv.leerItems(a.get(ReglasTv.rutaBuscar(consulta))), r -> {
            if (!consulta.equals(p.consulta.trim()) || historial.actual() != p) return;
            p.items = new ArrayList<>(r);
            p.sinMas = true;
            ponerResultados(p);
        }, e -> {
            if (e instanceof ApiVelora.ErrorApi && ((ApiVelora.ErrorApi) e).estado == 401) entrada();
            else if (avisoBuscar != null) avisoBuscar.setText("No se ha podido buscar. Vuelve a intentarlo.");
        });
    }

    private void quitarResultados() {
        View r = contenido.findViewWithTag("buscar-resultados");
        if (r instanceof FrameLayout) ((FrameLayout) r).removeAllViews();
        rejilla = null;
        adaptador = null;
        enRejilla = null;
    }

    private void ponerResultados(Pantalla p) {
        quitarResultados();
        View r = contenido.findViewWithTag("buscar-resultados");
        if (!(r instanceof FrameLayout)) return;
        avisoBuscar.setText(p.items.isEmpty() ? "No hay resultados para «" + p.consulta.trim() + "»." : "");
        if (p.items.isEmpty()) return;
        GridView g = nuevaRejilla(p.items);
        enRejilla = p;
        ((FrameLayout) r).addView(g);
        g.setSelection(Math.min(p.posicion, p.items.size() - 1));
    }

    private void ocultarTeclado() {
        InputMethodManager t = (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
        if (t != null && campoBuscar != null) t.hideSoftInputFromWindow(campoBuscar.getWindowToken(), 0);
    }

    // ------- Ficha -------

    private void pedirFicha(Pantalla p) {
        ApiVelora a = api;
        cargar(() -> ReglasTv.leerFicha(a.get("/api/v1/items/" + ReglasTv.cifrar(p.id))), f -> pintarFicha(p, f), e -> fallo(e, () -> mostrar(p)));
    }

    private void pintarFicha(Pantalla p, ReglasTv.Ficha f) {
        vaciar();
        ImageView fondoFicha = new ImageView(this);
        fondoFicha.setScaleType(ImageView.ScaleType.CENTER_CROP);
        contenido.addView(fondoFicha, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        imagenes.poner(fondoFicha, f.fondo ? ReglasTv.imagen(f.id, "backdrop", f.rev) : null, anchoPantalla(), altoPantalla());
        View velo = new View(this);
        velo.setBackground(VistasTv.degradado(GradientDrawable.Orientation.LEFT_RIGHT, Color.argb(245, 14, 17, 22), Color.argb(90, 14, 17, 22)));
        contenido.addView(velo, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        LinearLayout hoja = new LinearLayout(this);
        hoja.setPadding(dp(24), dp(20), dp(24), dp(16));
        contenido.addView(hoja, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        if (f.poster) {
            ImageView caratula = new ImageView(this);
            caratula.setScaleType(ImageView.ScaleType.CENTER_CROP);
            caratula.setBackgroundColor(VistasTv.ELEVADO);
            int ancho = dp(128);
            int alto = ancho * 3 / 2;
            imagenes.poner(caratula, ReglasTv.imagen(f.id, "poster", f.rev), ancho, alto);
            LinearLayout.LayoutParams lc = new LinearLayout.LayoutParams(ancho, alto);
            lc.setMarginEnd(dp(20));
            hoja.addView(caratula, lc);
        }
        LinearLayout c = new LinearLayout(this);
        c.setOrientation(LinearLayout.VERTICAL);
        ScrollView detalle = new ScrollView(this);
        detalle.setFillViewport(true);
        detalle.setClipToPadding(false);
        hoja.addView(detalle, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.MATCH_PARENT, 1));
        detalle.addView(c, new ScrollView.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        TextView titulo = VistasTv.titulo(this, f.titulo, 22);
        titulo.setMaxLines(2);
        titulo.setEllipsize(android.text.TextUtils.TruncateAt.END);
        c.addView(titulo);
        if (!f.lema.isEmpty()) {
            TextView lema = VistasTv.texto(this, f.lema, 13, VistasTv.APAGADO);
            lema.setMaxLines(1);
            lema.setEllipsize(android.text.TextUtils.TruncateAt.END);
            lema.setPadding(0, dp(4), 0, 0);
            c.addView(lema);
        }
        TextView datos = VistasTv.texto(this, ReglasTv.datosBasicos(f), 12, VistasTv.TEXTO);
        datos.setMaxLines(2);
        datos.setPadding(0, dp(6), 0, 0);
        c.addView(datos);
        if (f.reanudable()) {
            TextView queda = VistasTv.texto(this, ReglasTv.restante(f), 12, VistasTv.ACENTO);
            queda.setPadding(0, dp(4), 0, 0);
            c.addView(queda);
        }
        TextView sinopsis = VistasTv.texto(this, f.sinopsis, 13, VistasTv.TEXTO);
        sinopsis.setEllipsize(android.text.TextUtils.TruncateAt.END);
        sinopsis.setLineSpacing(dp(2), 1f);
        // Las acciones van justo debajo, y hay sitio para ellas con la sinopsis más larga: título, datos y botones siempre se ven a la vez.
        sinopsis.setMaxLines(altoPantalla() < dp(600) ? 10 : 15);
        sinopsis.setPadding(0, dp(10), 0, dp(12));
        c.addView(sinopsis);
        accionesFicha(p, f, c);
        informacionFicha(f, c);
        JSONObject presentacion = f.informacion == null ? null : f.informacion.optJSONObject("presentation");
        if (f.informacion != null && !f.informacionPedida && (presentacion == null || presentacion.length() == 0)) {
            f.informacionPedida = true;
            cargar(() -> new JSONObject(api.get("/api/v1/items/" + ReglasTv.cifrar(f.id) + "/presentation")), datosExtra -> {
                if (historial.actual() != p) return;
                try { f.informacion.put("presentation", datosExtra.optJSONObject("presentation")); } catch (org.json.JSONException ignorada) { return; }
                // Keep current actions and focus; only append the newly available optional information.
                c.removeView(c.findViewWithTag("informacion-ficha"));
                informacionFicha(f, c);
            }, error -> { /* La información opcional nunca bloquea la ficha. */ });
        }
    }

    private void informacionFicha(ReglasTv.Ficha f, LinearLayout padre) {
        if (f.informacion == null) return;
        LinearLayout c = new LinearLayout(this); c.setOrientation(LinearLayout.VERTICAL);
        c.setTag("informacion-ficha"); c.setPadding(0, dp(24), 0, dp(24)); padre.addView(c);
        JSONObject o = f.informacion, p = o.optJSONObject("presentation");
        JSONObject creditos = p == null ? null : p.optJSONObject("credits");
        if (creditos != null) {
            JSONArray reparto = creditos.optJSONArray("cast"), equipo = creditos.optJSONArray("crew");
            if ((reparto != null && reparto.length() > 0) || (equipo != null && equipo.length() > 0)) {
                c.addView(VistasTv.titulo(this, "Reparto y equipo técnico", 16));
                HorizontalScrollView scroll = new HorizontalScrollView(this);
                LinearLayout fila = new LinearLayout(this); scroll.addView(fila); c.addView(scroll);
                for (JSONArray lista : new JSONArray[]{reparto, equipo}) for (int i = 0; lista != null && i < lista.length(); i++) {
                    JSONObject persona = lista.optJSONObject(i); if (persona == null) continue;
                    LinearLayout tarjeta = new LinearLayout(this); tarjeta.setOrientation(LinearLayout.VERTICAL); tarjeta.setFocusable(true);
                    tarjeta.setPadding(dp(4), dp(8), dp(8), dp(8));
                    if (lista == reparto) {
                        VistasTv.recibeFoco(tarjeta);
                        tarjeta.setBackground(VistasTv.boton(this, "").getBackground());
                        tarjeta.setOnClickListener(v -> abrirWeb("/persona/" + persona.optInt("id") + "?nombre=" + ReglasTv.cifrar(persona.optString("name"))));
                    }
                    fila.addView(tarjeta, new LinearLayout.LayoutParams(dp(104), ViewGroup.LayoutParams.WRAP_CONTENT));
                    ImageView foto = new ImageView(this); foto.setScaleType(ImageView.ScaleType.CENTER_CROP); foto.setBackgroundColor(VistasTv.ELEVADO);
                    tarjeta.addView(foto, new LinearLayout.LayoutParams(dp(88), dp(132)));
                    String perfil = persona.optString("profile_path");
                    if (perfil.matches("/[a-zA-Z0-9]+\\.(jpg|png)")) imagenes.poner(foto, "/api/v1/items/" + f.id + "/people/" + persona.optInt("id") + "/image?v=" + ReglasTv.cifrar(perfil), dp(88), dp(132));
                    tarjeta.addView(VistasTv.texto(this, persona.optString("name"), 12, VistasTv.TEXTO));
                    tarjeta.addView(VistasTv.texto(this, persona.optString("character", persona.optString("job")), 11, VistasTv.APAGADO));
                }
            }
        }
        c.addView(VistasTv.titulo(this, "Acerca de " + f.titulo, 16));
        info(c, "Título original", o.optString("original_title", ""));
        if (o.optDouble("community_rating", 0) > 0) info(c, "Puntuación TMDB", String.format(java.util.Locale.ROOT, "%.1f / 10", o.optDouble("community_rating")));
        info(c, "Géneros", android.text.TextUtils.join(" · ", f.generos));
        if (p != null) {
            info(c, "Idioma original", p.optString("original_language", ""));
            for (String campo : new String[]{"production_companies", "production_countries"}) {
                JSONArray lista = p.optJSONArray(campo); List<String> nombres = new ArrayList<>();
                for (int i = 0; lista != null && i < lista.length(); i++) { JSONObject v = lista.optJSONObject(i); if (v != null) nombres.add(v.optString("name")); }
                info(c, campo.equals("production_companies") ? "Estudios" : "Países de producción", android.text.TextUtils.join(" · ", nombres));
            }
        }
        JSONArray archivos = o.optJSONArray("files");
        for (int i = 0; archivos != null && i < archivos.length(); i++) {
            JSONObject archivo = archivos.optJSONObject(i); if (archivo == null) continue;
            TextView bloque = VistasTv.titulo(this, "Información multimedia" + (archivos.length() > 1 ? " · Versión " + (i+1) : ""), 16);
            bloque.setPadding(0, dp(20), 0, dp(8)); c.addView(bloque);
            info(c, "Contenedor", archivo.optString("container", "").toUpperCase(java.util.Locale.ROOT));
            info(c, "Tamaño", String.format(java.util.Locale.ROOT, "%.2f GiB", archivo.optLong("size_bytes") / Math.pow(1024, 3)));
            long duracion = archivo.optLong("duration_ms");
            if (duracion > 0) info(c, "Bitrate medio", String.format(java.util.Locale.ROOT, "%.2f Mb/s", archivo.optLong("size_bytes") * 8.0 / duracion / 1000));
            JSONArray pistas = archivo.optJSONArray("streams");
            for (int j = 0; pistas != null && j < pistas.length(); j++) {
                JSONObject pista = pistas.optJSONObject(j); if (pista == null) continue;
                String tipo = pista.optString("kind"), valor = pista.optString("codec").toUpperCase(java.util.Locale.ROOT);
                String idioma = pista.optString("language", ""); if (!idioma.isEmpty()) valor += " · " + idioma;
                if (pista.optInt("width") > 0) valor += " · " + pista.optInt("width") + " × " + pista.optInt("height");
                if (pista.optInt("channels") > 0) valor += " · " + pista.optInt("channels") + " canales";
                if (pista.optInt("bit_depth") > 0) valor += " · " + pista.optInt("bit_depth") + " bits";
                String hdr = pista.optString("hdr", ""); if (!hdr.isEmpty()) valor += " · " + hdr.toUpperCase(java.util.Locale.ROOT);
                info(c, tipo.equals("video") ? "Vídeo" : tipo.equals("audio") ? "Audio" : "Subtítulos", valor);
            }
        }
    }
    private void info(LinearLayout c, String etiqueta, String valor) {
        if (valor == null || valor.isEmpty() || valor.equals("null")) return;
        TextView t = VistasTv.texto(this, etiqueta + ": " + valor, 12, VistasTv.APAGADO);
        t.setPadding(0, dp(6), 0, dp(4)); t.setFocusable(true); c.addView(t);
    }

    private void accionesFicha(Pantalla p, ReglasTv.Ficha f, LinearLayout c) {
        final ReglasTv.Archivo[] archivo = {ReglasTv.archivoInicial(f)};
        final int[] audio = {-1};
        final String[] subtitulo = {""};
        final String[] archivoSubtitulo = {archivo[0] == null ? "" : archivo[0].id};
        boolean sePuede = archivo[0] != null && !archivo[0].falta;
        if (!sePuede) {
            TextView aviso = VistasTv.texto(this, "El archivo de esta película no está disponible ahora mismo.", 13, VistasTv.ACENTO);
            aviso.setPadding(0, 0, 0, dp(8));
            c.addView(aviso);
        }
        HorizontalScrollView desplazar = new HorizontalScrollView(this);
        desplazar.setHorizontalScrollBarEnabled(false);
        desplazar.setOverScrollMode(View.OVER_SCROLL_NEVER);
        LinearLayout b = new LinearLayout(this);
        desplazar.addView(b);
        View primero = null;
        if (sePuede) {
            TextView play = accion(b, f.reanudable() ? "Continuar" : "Reproducir", v -> reproducir(f.id, archivo[0].id, audio[0], false, archivo[0].id.equals(archivoSubtitulo[0]) ? subtitulo[0] : ""));
            play.setTag("ficha-reproducir");
            primero = play;
            if (f.reanudable()) accion(b, "Desde el principio", v -> reproducir(f.id, archivo[0].id, audio[0], true, archivo[0].id.equals(archivoSubtitulo[0]) ? subtitulo[0] : "")).setTag("ficha-inicio");
        }
        String trailer = ReglasTv.trailer(f);
        if (trailer != null) accion(b, "Tráiler", v -> reproducir(f.id, trailer, -1, false)).setTag("ficha-trailer");
        TextView vista = accion(b, f.vista ? "Marcar como no vista" : "Marcar como vista", v -> cambiarVista(f.id, !f.vista));
        vista.setTag("ficha-vista");
        if (primero == null) primero = vista;
        if (f.extras.size() > 1 || (f.extras.size() == 1 && trailer == null)) accion(b, "Extras", v -> elegirExtra(f)).setTag("ficha-extras");
        c.addView(desplazar, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        if (sePuede) {
            TextView archivoTitulo = VistasTv.titulo(this, "Archivo original", 16); archivoTitulo.setPadding(0,dp(20),0,dp(6)); c.addView(archivoTitulo);
            c.addView(VistasTv.texto(this, ReglasTv.nombreVersion(archivo[0], 0), 12, VistasTv.TEXTO));
            HorizontalScrollView scrollOpciones = new HorizontalScrollView(this); LinearLayout opciones = new LinearLayout(this); scrollOpciones.addView(opciones); c.addView(scrollOpciones);
        if (f.archivos.size() > 1 && sePuede) {
            TextView[] boton = new TextView[1];
            boton[0] = accion(opciones, "Versión: " + ReglasTv.nombreVersion(archivo[0], 0), v -> elegirVersion(f, archivo, audio, boton[0], () -> {
                subtitulo[0]=""; archivoSubtitulo[0]=archivo[0].id;
                TextView subt=c.findViewWithTag("ficha-subtitulos");if(subt!=null)subt.setText("Subtítulos: desactivados");
                TextView aud=c.findViewWithTag("ficha-audio");if(aud!=null)aud.setText("Audio");
            }));
            boton[0].setTag("ficha-version");
        }
        if (sePuede) {
            TextView[] boton = new TextView[1];
            boton[0] = accion(opciones, "Audio", v -> elegirAudio(archivo[0], audio, boton[0]));
            boton[0].setTag("ficha-audio");
        }
            TextView[] botonSub = new TextView[1];
            botonSub[0] = accion(opciones, "Subtítulos: desactivados", v -> {
                String seleccionado = archivo[0].id;
                cargar(() -> new JSONObject(api.get("/api/v1/stream/files/" + seleccionado + "/subtitles")), respuesta -> {
                    if (!seleccionado.equals(archivo[0].id)) return;
                    JSONArray pistas = respuesta.optJSONArray("subtitles"); int n = pistas == null ? 0 : pistas.length();
                    String[] nombres = new String[n+1]; nombres[0] = "Desactivados";
                    for(int j=0;j<n;j++) nombres[j+1] = pistas.optJSONObject(j).optString("label", "Subtítulos");
                    new AlertDialog.Builder(this).setTitle("Subtítulos").setItems(nombres, (d,w) -> {
                        archivoSubtitulo[0] = seleccionado;
                        subtitulo[0] = w == 0 ? "off" : pistas.optJSONObject(w-1).optString("id");
                        botonSub[0].setText("Subtítulos: " + nombres[w]);
                    }).show();
                }, error -> Toast.makeText(this,"No se han podido consultar los subtítulos.",Toast.LENGTH_LONG).show());
            });
            botonSub[0].setTag("ficha-subtitulos");
        }
        enfocar(p.foco, primero);
    }

    private void elegirVersion(ReglasTv.Ficha f, ReglasTv.Archivo[] archivo, int[] audio, TextView boton, Runnable cambio) {
        String[] nombres = new String[f.archivos.size()];
        for (int i = 0; i < nombres.length; i++) nombres[i] = ReglasTv.nombreVersion(f.archivos.get(i), i);
        new AlertDialog.Builder(this).setTitle("Versión").setItems(nombres, (d, w) -> {
            archivo[0] = f.archivos.get(w);
            audio[0] = -1; // Las pistas de otro archivo son otras.
            boton.setText("Versión: " + nombres[w]);
            cambio.run();
        }).show();
    }

    private void elegirAudio(ReglasTv.Archivo archivo, int[] audio, TextView boton) {
        String[] nombres = new String[archivo.audios.size()+1]; nombres[0]="Audio por defecto";
        for (int i=0;i<archivo.audios.size();i++) nombres[i+1]=ReglasTv.nombreAudio(archivo.audios.get(i));
        new AlertDialog.Builder(this).setTitle("Audio").setItems(nombres, (d,w) -> {
            audio[0] = w==0 ? -1 : archivo.audios.get(w-1).indice;
            boton.setText("Audio: " + nombres[w]);
        }).show();
    }

    private void elegirExtra(ReglasTv.Ficha f) {
        String[] nombres = new String[f.extras.size()];
        for (int i = 0; i < nombres.length; i++) nombres[i] = f.extras.get(i).nombre;
        new AlertDialog.Builder(this).setTitle("Extras")
                .setItems(nombres, (d, w) -> reproducir(f.id, f.extras.get(w).id, -1, false)).show();
    }

    // ------- Reproducción -------

    private void reproducir(String item, String archivo, int audio, boolean desdeInicio) { reproducir(item, archivo, audio, desdeInicio, ""); }

    private void reproducir(String item, String archivo, int audio, boolean desdeInicio, String sub) {
        guardarFoco();
        try {
            Intent i = new Intent(this, ReproductorActivity.class);
            i.putExtra(ReproductorActivity.DATOS, new JSONObject(ReglasTv.datosReproductor(item, archivo, audio, desdeInicio, aparato)).put("sub",sub).toString());
            i.putExtra(ReproductorActivity.ORIGEN, origen);
            i.putExtra(ReproductorActivity.COOKIES, CookieManager.getInstance().getCookie(origen));
            startActivityForResult(i, REPRODUCIR);
        } catch (org.json.JSONException imposible) {
            Toast.makeText(this, "No se ha podido abrir el vídeo.", Toast.LENGTH_LONG).show();
        }
    }

    @Override
    protected void onActivityResult(int peticion, int resultado, Intent datos) {
        if (peticion != REPRODUCIR) return;
        if (resultado == ReproductorActivity.RESULTADO_FALLO && datos != null) {
            Toast.makeText(this, "Se abre el reproductor alternativo.", Toast.LENGTH_LONG).show();
            abrirWeb("/ver/" + Uri.encode(datos.getStringExtra(ReproductorActivity.ITEM)) + "?web=1&file=" + Uri.encode(datos.getStringExtra(ReproductorActivity.ARCHIVO)));
        } else if (historial != null) {
            mostrar(historial.actual()); // El progreso ha cambiado; el foco vuelve a donde estaba.
        }
    }

    private void abrirWeb(String ruta) {
        Intent i = new Intent(this, MainActivity.class);
        i.putExtra(MainActivity.RUTA, ruta);
        startActivity(i);
    }

    // ------- Entrada -------

    private void entrada() {
        generacion++;
        vaciar();
        lateral.setVisibility(View.GONE);
        enEntrada = true;
        formularioEntrada = false;
        cargando();
        ApiVelora a = new ApiVelora(origen, "", aparato);
        cargar(() -> ReglasTv.leerPerfiles(a.get("/api/v1/auth/profiles")), this::pintarPerfiles, e -> {
            if (e instanceof ApiVelora.ErrorApi) pintarPerfiles(new ArrayList<>());
            else fallo(e, this::entrada);
        });
    }

    private void pintarPerfiles(List<ReglasTv.Perfil> perfiles) {
        sinPerfiles = perfiles.isEmpty();
        if (sinPerfiles) {
            pintarFormulario(null);
            return;
        }
        formularioEntrada = false;
        vaciar();
        LinearLayout c = centrado();
        TextView t = VistasTv.titulo(this, "¿Quién está viendo?", 20);
        t.setPadding(0, 0, 0, dp(16));
        c.addView(t);
        LinearLayout lista = new LinearLayout(this);
        lista.setGravity(Gravity.CENTER);
        View primero = null;
        for (ReglasTv.Perfil p : perfiles) {
            View b = accion(lista, p.nombre, v -> pintarFormulario(p));
            if (primero == null) primero = b;
        }
        accion(lista, "Otro usuario", v -> pintarFormulario(null));
        HorizontalScrollView desplazar = new HorizontalScrollView(this);
        desplazar.setHorizontalScrollBarEnabled(false);
        desplazar.addView(lista);
        c.addView(desplazar);
        if (primero != null) primero.requestFocus();
    }

    private void pintarFormulario(ReglasTv.Perfil perfil) {
        formularioEntrada = true;
        vaciar();
        LinearLayout c = centrado();
        c.setPadding(dp(240), dp(16), dp(240), dp(16));
        TextView t = VistasTv.titulo(this, perfil == null ? "Iniciar sesión" : perfil.nombre, 20);
        t.setGravity(Gravity.CENTER);
        t.setPadding(0, 0, 0, dp(14));
        c.addView(t, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        EditText nombre = null;
        if (perfil == null) {
            nombre = campoEntrada("Usuario", InputType.TYPE_CLASS_TEXT);
            c.addView(nombre, margenCampo());
        }
        EditText clave = campoEntrada("Contraseña", InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        c.addView(clave, margenCampo());
        TextView error = VistasTv.texto(this, "", 13, VistasTv.ACENTO);
        error.setGravity(Gravity.CENTER);
        error.setPadding(0, 0, 0, dp(8));
        c.addView(error, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        LinearLayout botones = new LinearLayout(this);
        botones.setGravity(Gravity.CENTER);
        EditText campoNombre = nombre;
        accion(botones, "Entrar", v -> entrar(perfil, campoNombre, clave, error)).setTag("entrada-entrar");
        c.addView(botones, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        (nombre != null ? nombre : clave).requestFocus();
    }

    private EditText campoEntrada(String pista, int tipo) {
        EditText e = new EditText(this);
        e.setHint(pista);
        e.setHintTextColor(VistasTv.APAGADO);
        e.setTextColor(VistasTv.TEXTO);
        e.setTextSize(14);
        e.setSingleLine(true);
        e.setInputType(tipo);
        e.setBackground(fondoCampo());
        e.setPadding(dp(12), dp(8), dp(12), dp(8));
        return e;
    }

    private LinearLayout.LayoutParams margenCampo() {
        LinearLayout.LayoutParams l = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        l.bottomMargin = dp(10);
        return l;
    }

    private void entrar(ReglasTv.Perfil perfil, EditText nombre, EditText clave, TextView error) {
        String cuerpo;
        try {
            cuerpo = ReglasTv.credenciales(perfil == null ? null : perfil.id, nombre == null ? "" : nombre.getText().toString(), clave.getText().toString());
        } catch (org.json.JSONException imposible) {
            return;
        }
        error.setText("Entrando…");
        ApiVelora a = new ApiVelora(origen, "", aparato);
        cargar(() -> a.entrar(cuerpo, android.os.Build.MODEL), cookies -> {
            CookieManager gestor = CookieManager.getInstance();
            gestor.setAcceptCookie(true);
            for (String c : cookies) gestor.setCookie(origen, c);
            gestor.flush();
            arrancar();
        }, e -> {
            int estado = e instanceof ApiVelora.ErrorApi ? ((ApiVelora.ErrorApi) e).estado : 0;
            error.setText(estado == 401 ? "Usuario o contraseña incorrectos."
                    : estado == 429 ? "Demasiados intentos. Espera un poco."
                    : "No se ha podido iniciar sesión.");
            clave.setText("");
            clave.requestFocus();
        });
    }

    // ------- Ciclo de vida -------

    @Override
    protected void onResume() {
        super.onResume();
        pantallaCompleta();
        actualizador.continuar();
        actualizador.comprobar(origen);
    }

    @Override
    protected void onPause() {
        CookieManager.getInstance().flush();
        super.onPause();
    }

    /** Salir de la pantalla por cualquier motivo no es un cierre: un fallo al construirla nunca llega hasta aquí. */
    @Override
    protected void onStop() {
        hilo.removeCallbacks(darPorEstable);
        GuardianTv.estable(this);
        super.onStop();
    }

    @Override
    protected void onDestroy() {
        hilo.removeCallbacksAndMessages(null);
        actualizador.cerrar();
        imagenes.cerrar();
        fondo.shutdownNow();
        super.onDestroy();
    }
}
