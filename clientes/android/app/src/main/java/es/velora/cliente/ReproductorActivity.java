package es.velora.cliente;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.content.res.ColorStateList;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.ColorDrawable;
import android.graphics.drawable.GradientDrawable;
import android.graphics.drawable.StateListDrawable;
import android.media.MediaCodecInfo;
import android.media.MediaCodecList;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowManager;
import android.widget.FrameLayout;
import android.widget.ImageButton;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.ImageView;
import androidx.annotation.OptIn;
import androidx.media3.common.C;
import androidx.media3.common.Format;
import androidx.media3.common.MediaItem;
import androidx.media3.common.PlaybackException;
import androidx.media3.common.Player;
import androidx.media3.common.TrackSelectionOverride;
import androidx.media3.common.Tracks;
import androidx.media3.common.util.UnstableApi;
import androidx.media3.datasource.DefaultHttpDataSource;
import androidx.media3.datasource.HttpDataSource;
import androidx.media3.exoplayer.DefaultLoadControl;
import androidx.media3.exoplayer.DefaultRenderersFactory;
import androidx.media3.exoplayer.ExoPlaybackException;
import androidx.media3.exoplayer.ExoPlayer;
import androidx.media3.exoplayer.audio.AudioCapabilities;
import androidx.media3.exoplayer.audio.AudioSink;
import androidx.media3.exoplayer.audio.AudioOffloadSupport;
import androidx.media3.exoplayer.audio.DefaultAudioSink;
import androidx.media3.exoplayer.audio.ForwardingAudioSink;
import androidx.media3.common.MimeTypes;
import androidx.media3.common.audio.AudioProcessor;
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory;
import androidx.media3.ui.PlayerView;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

/**
 * Reproductor propio de la app de TV, con Media3: abre el archivo tal cual siempre que el aparato sepa decodificarlo y solo pide al
 * servidor que lo reempaquete cuando no. Mando: OK pausa, izquierda y derecha saltan, arriba o abajo llevan a los botones, Atrás sale.
 * Si falla, prueba a reempaquetar y a reabrir un número limitado de veces; si no, devuelve RESULTADO_FALLO y la app abre el reproductor web.
 */
@OptIn(markerClass = UnstableApi.class)
public final class ReproductorActivity extends Activity {
    static final String DATOS = "datos";
    static final String ORIGEN = "origen";
    static final String COOKIES = "cookies";
    static final String ITEM = "item";
    static final String ARCHIVO = "archivo";
    static final int RESULTADO_FALLO = RESULT_FIRST_USER;
    /** El servidor ha bloqueado el perfil: la web debe pedir el PIN. */
    static final int RESULTADO_BLOQUEADO = RESULT_FIRST_USER + 1;

    private static final int AZUL = Color.parseColor("#8FB0FF");
    private static final int SOBRE_AZUL = Color.parseColor("#0B1020");
    private static final int FONDO = Color.parseColor("#E6252B36");
    private static final long OCULTAR_MS = 4000;
    private static final long GUARDAR_MS = 10000;
    private static final long CONFIRMAR_SALTO_MS = 350;
    private static final long ATRAS_MS = 10000;
    private static final long ADELANTE_MS = 30000;

    private final Handler hilo = new Handler(Looper.getMainLooper());
    private final ExecutorService fondo = Executors.newSingleThreadExecutor();
    /** Subtítulos y capítulos van aparte: nunca hacen esperar a la imagen ni a un salto. */
    private final ExecutorService auxiliar = Executors.newFixedThreadPool(2);
    private final ApiVelora.Cancelable consultaListas = new ApiVelora.Cancelable();
    /** La preparación y las decisiones del servidor: se cancelan al cerrar el reproductor o pasar al episodio siguiente. */
    private final ApiVelora.Cancelable consultaPreparar = new ApiVelora.Cancelable();
    /** La consulta del punto de inicio del último salto en un flujo; un salto nuevo la cancela. */
    private ApiVelora.Cancelable consultaSalto;
    private final ReglasReproductor.Recuperacion recuperacion = new ReglasReproductor.Recuperacion();

    private ApiVelora api;
    private ExoPlayer player;
    private JSONObject caps;
    private String itemId;
    private String archivoId;
    private boolean esExtra;
    /** E2: el episodio que sigue a este («» si no hay o es un extra). */
    private volatile String siguienteId = "";
    /** Tramo final que hace de créditos cuando el episodio no los tiene marcados. */
    private JSONObject finalVirtual;
    private ReglasReproductor.Plan plan;
    private long offsetMs;
    private int intento;
    private int audioActual = -1;
    private int audioPedido = -1;
    private int audioPorAplicar = -1;
    private String subtituloPedido = "";
    private volatile int apertura; // Solo vale el resultado de la última apertura pedida.
    private boolean sinAplicarAudio;
    private long objetivoMs = -1;
    private long ultimoGuardado;
    private long ultimoLatido;
    private boolean presenciaVisible;
    private final String presenciaId = java.util.UUID.randomUUID().toString();
    private boolean terminado;

    private final List<JSONObject> audios = new ArrayList<>();
    private final List<JSONObject> subtitulos = new ArrayList<>();
    private final List<JSONObject> capitulos = new ArrayList<>();
    private List<ReglasReproductor.Cue> cues = Collections.emptyList();
    private String subActual;
    private JSONObject especial;

    private FrameLayout raiz;
    private LinearLayout controles;
    private LinearLayout botones;
    private TextView titulo;
    private ImageView logo;
    private LinearLayout cabecera;
    private TextView numeroEpisodio;
    private TextView nombreEpisodio;
    private TextView nombreSerie;
    private final Imagenes imagenes = new Imagenes();
    private TextView tiempo;
    private TextView restante;
    private TextView textoSub;
    private ApiVelora.Cancelable cargaSubtitulos;
    private TextView saltar;
    private TextView cancelarSiguiente;
    private LinearLayout avisos;
    private boolean siguienteCancelado;
    private long siguienteLimite = -1;
    private JSONObject ultimoAviso;
    private ProgressBar barra;
    private ProgressBar cargando;
    private ImageButton reproducirPausa;
    private ScrollView panel;
    private boolean enBotones;
    private boolean controlesSoloManual;
    private boolean activandoEspecial;

    private final Runnable ocultar = this::ocultarControles;
    private final Runnable aplicarSalto = this::aplicarSaltoPendiente;
    private final Runnable latido = new Runnable() {
        @Override
        public void run() {
            refrescar();
            hilo.postDelayed(this, 250);
        }
    };

    @Override
    protected void onCreate(Bundle guardado) {
        super.onCreate(guardado);
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        getWindow().getDecorView().setSystemUiVisibility(View.SYSTEM_UI_FLAG_FULLSCREEN | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION | View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION | View.SYSTEM_UI_FLAG_LAYOUT_STABLE);
        JSONObject datos;
        try {
            datos = new JSONObject(getIntent().getStringExtra(DATOS));
            caps = detectarCaps();
        } catch (JSONException | RuntimeException e) {
            finish();
            return;
        }
        controlesSoloManual = datos.optBoolean("controles_ocultos");
        itemId = datos.optString("item");
        archivoId = datos.optString("file");
        audioPedido = datos.optInt("audio", -1);
        subtituloPedido = datos.optString("sub", "");
        if (subtituloPedido.equals("auto")) subtituloPedido="";
        sinAplicarAudio = audioPedido >= 0;
        api = new ApiVelora(getIntent().getStringExtra(ORIGEN), getIntent().getStringExtra(COOKIES), datos.optString("device"));
        imagenes.sesion(getIntent().getStringExtra(ORIGEN), api.cabeceras());
        boolean desdeInicio = datos.optBoolean("inicio");
        construirInterfaz();
        if (controlesSoloManual) ocultarPorAviso();
        crearReproductor();
        hilo.post(latido);
        enFondo(() -> preparar(desdeInicio));
    }

    /** Una tarea de red puede acabar cuando ya se ha salido de esta pantalla: su resultado se descarta. */
    private void enPrincipal(Runnable r) {
        hilo.post(() -> {
            if (activa()) r.run();
        });
    }

    private void enFondo(Runnable r) {
        if (!fondo.isShutdown()) fondo.execute(r);
    }

    private void enAuxiliar(Runnable r) {
        if (!auxiliar.isShutdown()) auxiliar.execute(r);
    }

    private boolean activa() {
        return player != null && !isFinishing() && !isDestroyed();
    }

    private JSONArray audioPCM;

    private JSONObject detectarCaps() throws JSONException {
        Set<String> tipos = new HashSet<>();
        Map<String, Integer> anchos = new HashMap<>();
        Map<String, Integer> bits = new HashMap<>();
        Map<String, Integer> altos = new HashMap<>(); // la mayor altura que decodifica algún decodificador de cada tipo
        for (MediaCodecInfo info : new MediaCodecList(MediaCodecList.REGULAR_CODECS).getCodecInfos()) {
            if (info.isEncoder()) continue;
            for (String tipo : info.getSupportedTypes()) {
                tipos.add(tipo);
                try {
                    MediaCodecInfo.CodecCapabilities capabilities = info.getCapabilitiesForType(tipo);
                    MediaCodecInfo.VideoCapabilities v = capabilities.getVideoCapabilities();
                    if (v != null) {
                        altos.merge(tipo, v.getSupportedHeights().getUpper(), Math::max);
                        anchos.merge(tipo, v.getSupportedWidths().getUpper(), Math::max);
                        int profundidad = 8;
                        for (MediaCodecInfo.CodecProfileLevel nivel : capabilities.profileLevels) {
                            int perfil = nivel.profile;
                            if ((tipo.equals("video/avc") && (perfil == MediaCodecInfo.CodecProfileLevel.AVCProfileHigh10 || perfil == MediaCodecInfo.CodecProfileLevel.AVCProfileHigh422 || perfil == MediaCodecInfo.CodecProfileLevel.AVCProfileHigh444))
                                    || (tipo.equals("video/hevc") && perfil != MediaCodecInfo.CodecProfileLevel.HEVCProfileMain)
                                    || (tipo.equals("video/x-vnd.on2.vp9") && (perfil == MediaCodecInfo.CodecProfileLevel.VP9Profile2 || perfil == MediaCodecInfo.CodecProfileLevel.VP9Profile3 || perfil == MediaCodecInfo.CodecProfileLevel.VP9Profile2HDR || perfil == MediaCodecInfo.CodecProfileLevel.VP9Profile2HDR10Plus))
                                    || (tipo.equals("video/av01") && perfil != MediaCodecInfo.CodecProfileLevel.AV1ProfileMain8)) profundidad = 10;
                        }
                        bits.merge(tipo, profundidad, Math::max);
                    }
                } catch (RuntimeException sinDatos) {
                    // Sin datos de este decodificador: no limita nada.
                }
            }
        }
        // Lo que el aparato no decodifica pero sí puede enviar tal cual al televisor o al equipo de sonido.
        AudioCapabilities sonido = AudioCapabilities.getCapabilities(this);
        List<String> directo = new ArrayList<>();
        if (sonido.supportsEncoding(C.ENCODING_AC3)) directo.add("ac3");
        if (sonido.supportsEncoding(C.ENCODING_E_AC3)) directo.add("eac3");
        if (sonido.supportsEncoding(C.ENCODING_DTS) || sonido.supportsEncoding(C.ENCODING_DTS_HD)) directo.add("dts");
        if (sonido.supportsEncoding(C.ENCODING_DOLBY_TRUEHD)) directo.add("truehd");
        audioPCM = ReglasReproductor.caps(tipos, Collections.emptyList(), altos).getJSONArray("audio");
        JSONObject resultado = ReglasReproductor.caps(tipos, directo, altos);
        boolean hdr10 = false;
        if (android.os.Build.VERSION.SDK_INT >= 24) {
            try {
                android.view.Display.HdrCapabilities hdrCaps = getWindowManager().getDefaultDisplay().getHdrCapabilities();
                if (hdrCaps != null) for (int hdr : hdrCaps.getSupportedHdrTypes())
                    if (hdr == android.view.Display.HdrCapabilities.HDR_TYPE_HDR10) hdr10 = true;
            } catch (RuntimeException sinDatosHdr) { }
        }
        resultado.put("hdr10", hdr10);
        ReglasReproductor.limites(resultado, "max_widths", anchos);
        ReglasReproductor.limites(resultado, "max_bit_depths", bits);
        return resultado;
    }

    private volatile boolean audioPCMActivo=true;
    private int revisionModoAudio;
    private final VolumenAutomatico volumenAutomatico = new VolumenAutomatico();

    private void crearReproductor() {
        // Margen amplio de memoria intermedia: una bajada de la red o del disco no debe verse como un parón.
        DefaultLoadControl memoria = new DefaultLoadControl.Builder()
                .setBufferDurationsMs(30000, 60000, 2500, 5000)
                .setPrioritizeTimeOverSizeThresholds(true)
                .build();
        DefaultHttpDataSource.Factory red = new DefaultHttpDataSource.Factory()
                .setDefaultRequestProperties(api.cabeceras())
                .setConnectTimeoutMs(8000)
                .setReadTimeoutMs(30000)
                .setUserAgent("VeloraCliente");
        player = new ExoPlayer.Builder(this, new DefaultRenderersFactory(this) {
            @Override protected AudioSink buildAudioSink(android.content.Context context, boolean floating, boolean speed) {
                AudioSink sink = new DefaultAudioSink.Builder(context)
                        .setEnableFloatOutput(false)
                        .setEnableAudioTrackPlaybackParams(speed)
                        .setAudioProcessors(new AudioProcessor[]{volumenAutomatico}).build();
                return new ForwardingAudioSink(sink) {
                    @Override public void configure(Format format,int bufferSize,int[] channels) throws AudioSink.ConfigurationException {
                        audioPCMActivo=MimeTypes.AUDIO_RAW.equals(format.sampleMimeType);
                        super.configure(format,bufferSize,channels);
                    }
                    @Override public AudioOffloadSupport getFormatOffloadSupport(Format format) {
                        return volumenAutomatico.habilitado() ? AudioOffloadSupport.DEFAULT_UNSUPPORTED : super.getFormatOffloadSupport(format);
                    }
                    @Override public boolean supportsFormat(Format format) { return getFormatSupport(format) != SINK_FORMAT_UNSUPPORTED; }
                    @Override public int getFormatSupport(Format format) {
                        // PCM is required for DSP; encoded passthrough would bypass it.
                        if (volumenAutomatico.habilitado() && !MimeTypes.AUDIO_RAW.equals(format.sampleMimeType)) return SINK_FORMAT_UNSUPPORTED;
                        return super.getFormatSupport(format);
                    }
                };
            }
        }.setEnableDecoderFallback(true))
                .setLoadControl(memoria)
                .setMediaSourceFactory(new DefaultMediaSourceFactory(red))
                .build();
        PlayerView vista = new PlayerView(this);
        vista.setUseController(false);
        vista.setFocusable(false);
        vista.setPlayer(player);
        raiz.addView(vista, 0, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        player.setPlayWhenReady(true);
        player.setTrackSelectionParameters(player.getTrackSelectionParameters().buildUpon().setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true).build());
        player.addListener(new Player.Listener() {
            @Override
            public void onPlaybackStateChanged(int estado) {
                cargando.setVisibility(estado == Player.STATE_BUFFERING ? View.VISIBLE : View.GONE);
                if (estado == Player.STATE_ENDED) alTerminar();
            }

            @Override
            public void onIsPlayingChanged(boolean sonando) {
                reproducirPausa.setImageResource(sonando ? R.drawable.ic_pause_relleno : R.drawable.ic_play_relleno);
                if (sonando) programarOcultar();
                else if (player.getPlaybackState() == Player.STATE_READY) { // Una pausa de verdad, no una espera a que lleguen datos.
                    mostrarControles();
                    guardarProgreso();
                }
            }

            @Override
            public void onTracksChanged(Tracks pistas) {
                aplicarSubtituloPedido();
                aplicarAudioInicial(pistas);
                if (sinAplicarAudio && !pistas.getGroups().isEmpty()) {
                    sinAplicarAudio = false;
                    if (audioPedido != audioActual) elegirAudio(audioPedido);
                }
            }

            @Override
            public void onPlayerError(PlaybackException error) {
                int http = codigoHttp(error);
                if (ReglasReproductor.perfilBloqueado(http)) {
                    salirBloqueado();
                    return;
                }
                if (ReglasReproductor.sinPermiso(http)) {
                    avisarYSalir("Ya no tienes acceso a este título.");
                    return;
                }
                Format pista = error instanceof ExoPlaybackException && ((ExoPlaybackException) error).type == ExoPlaybackException.TYPE_RENDERER
                        ? ((ExoPlaybackException) error).rendererFormat : null;
                recuperar(ReglasReproductor.causa(error.errorCode, http, pista == null ? null : pista.sampleMimeType), pista);
            }
        });
    }

    /** Prepara lo que no depende de la imagen. Va en segundo plano. */
    private void preparar(boolean desdeInicio) {
        try {
            JSONObject item = new JSONObject(api.get("/api/v1/items/" + itemId, consultaPreparar, 30000)).getJSONObject("item");
            JSONArray archivos = item.optJSONArray("files");
            if (archivoId.isEmpty()) archivoId = primerArchivo(archivos);
            JSONArray extras = item.optJSONArray("extras");
            for (int i = 0; extras != null && i < extras.length(); i++) {
                if (archivoId.equals(extras.getJSONObject(i).optString("id"))) esExtra = true;
            }
            // E2: al terminar un episodio empieza el siguiente (nunca tras un tráiler o un extra).
            JSONObject siguiente = item.optJSONObject("next_episode");
            siguienteId = esExtra || siguiente == null ? "" : siguiente.optString("id");
            cargarPistas(archivos);
            String archivo = archivoId;
            enAuxiliar(() -> cargarListas(archivo));
            ReglasReproductor.Plan p = decidir(null);
            long inicio = ReglasReproductor.inicioMs(item.optBoolean("played"), item.optLong("position_ms"), desdeInicio, esExtra);
            enPrincipal(() -> {
                aplicarCabecera(item);
                imagenes.poner(logo, item.optBoolean("has_logo") ? ReglasTv.imagen(itemId, "logo", item.optString("image_rev")) : null, dp(200), dp(72));
                if (!p.reproducible()) {
                    avisarYSalir(p.motivo.isEmpty() ? "Este archivo no se puede reproducir en este aparato." : p.motivo);
                    return;
                }
                abrir(p, inicio);
            });
        } catch (ApiVelora.ErrorApi e) {
            if (ReglasReproductor.perfilBloqueado(e.estado)) {
                enPrincipal(this::salirBloqueado);
                return;
            }
            enPrincipal(() -> avisarYSalir(e.estado == 429 ? "Se ha alcanzado el límite de reproducciones simultáneas. Detén otra reproducción e inténtalo de nuevo." : e.estado == 401 || e.estado == 404 ? "Ya no tienes acceso a este título." : "El servidor no ha podido preparar la reproducción."));
        } catch (Exception e) {
            enPrincipal(this::salirConFallo);
        }
    }

    /** Toda la identidad del episodio comparte cabecera y visibilidad con el logo. */
    private void aplicarCabecera(JSONObject item) {
        boolean episodio = "episode".equals(item.optString("kind")) && !esExtra;
        boolean tieneLogo = item.optBoolean("has_logo");
        String nombre = item.optString("title");
        String serie = item.optString("series_title");
        titulo.setText(nombre);
        titulo.setVisibility(episodio || (tieneLogo && !esExtra) ? View.GONE : View.VISIBLE);
        logo.setVisibility(tieneLogo ? View.VISIBLE : View.GONE);
        nombreSerie.setText(serie);
        nombreSerie.setVisibility(episodio && !tieneLogo && !serie.isEmpty() ? View.VISIBLE : View.GONE);
        String temporada = item.has("season_number") ? item.optInt("season_number") == 0 ? "Especiales" : "Temporada " + item.optInt("season_number") : "Temporada ?";
        String numero = item.has("episode_number") ? String.valueOf(item.optInt("episode_number")) : "?";
        if (item.optInt("episode_end") > 0) numero += "–" + item.optInt("episode_end");
        numeroEpisodio.setText(temporada + " · Episodio " + numero);
        numeroEpisodio.setVisibility(episodio ? View.VISIBLE : View.GONE);
        nombreEpisodio.setText(nombre);
        nombreEpisodio.setVisibility(episodio && !nombre.isEmpty() && !nombre.equals(serie) ? View.VISIBLE : View.GONE);
    }

    private static String primerArchivo(JSONArray archivos) throws JSONException {
        for (int i = 0; archivos != null && i < archivos.length(); i++) {
            JSONObject f = archivos.getJSONObject(i);
            if (!f.optBoolean("missing")) return f.getString("id");
        }
        throw new JSONException("sin archivos");
    }

    private void cargarPistas(JSONArray archivos) throws JSONException {
        for (int i = 0; archivos != null && i < archivos.length(); i++) {
            JSONObject f = archivos.getJSONObject(i);
            if (!archivoId.equals(f.optString("id"))) continue;
            JSONArray pistas = f.optJSONArray("streams");
            for (int j = 0; pistas != null && j < pistas.length(); j++) {
                if ("audio".equals(pistas.getJSONObject(j).optString("kind"))) audios.add(pistas.getJSONObject(j));
            }
        }
    }

    /**
     * Sin capítulos ni subtítulos también se ve la película: se piden mientras el vídeo ya carga, un fallo o una respuesta
     * lenta no lo detienen, y cada lista se publica en el hilo principal en cuanto llega. Al salir se cancelan.
     */
    private void cargarListas(String archivo) {
        enAuxiliar(() -> cargarSubtitulos(archivo));
        List<JSONObject> leidos = leerLista("/api/v1/stream/files/" + archivo + "/chapters", "chapters");
        enPrincipal(() -> {
            if (!archivo.equals(archivoId)) return;
            capitulos.clear();
            capitulos.addAll(leidos);
            refrescar();
        });
    }
    private void aplicarAudioInicial(Tracks pistas) {
        boolean hayAudio=false;
        for (Tracks.Group grupo:pistas.getGroups()) if(grupo.getType()==C.TRACK_TYPE_AUDIO) hayAudio=true;
        if (audioPorAplicar<0 || !hayAudio) return;
        int indice=audioPorAplicar;
        audioPorAplicar=-1;
        int orden=-1;
        for(int i=0;i<audios.size();i++) if(audios.get(i).optInt("index",-2)==indice) orden=i;
        if(orden>=0 && !seleccionarGrupo(C.TRACK_TYPE_AUDIO,orden)) {
            try {caps.put("source_audio_unsupported",true);caps.put("force_remux",true);} catch(JSONException imposible) { }
            elegirAudio(indice);
        }
    }

    private void cargarSubtitulos(String archivo) {
        List<JSONObject> subs = leerLista("/api/v1/stream/files/" + archivo + "/subtitles", "subtitles");
        enPrincipal(() -> {
            if (!archivo.equals(archivoId)) return;
            subtitulos.clear();
            subtitulos.addAll(subs);
            aplicarSubtituloPedido();
        });
    }

    private void aplicarSubtituloPedido() {
        if (subtituloPedido.isEmpty()) subtituloPedido=ReglasReproductor.subtituloForzadoEspanol(subtitulos);
        if (subtituloPedido.isEmpty() || subtituloPedido.equals("off")) return;
        int interno=0;
        for (JSONObject sub : subtitulos) {
            int orden = sub.optBoolean("external") ? -1 : interno++;
            if (!subtituloPedido.equals(sub.optString("id"))) continue;
            if (sub.optBoolean("supported")) {subtituloPedido="off";elegirSubtitulo(sub);}
            else if (plan != null && !plan.flujo() && !player.getCurrentTracks().getGroups().isEmpty()) {
                String id=subtituloPedido;subtituloPedido="off";elegirSubtituloDeImagen(id,orden);
            }
            return;
        }
    }

    private List<JSONObject> leerLista(String ruta, String clave) {
        List<JSONObject> lista = new ArrayList<>();
        try {
            JSONArray datos = new JSONObject(api.get(ruta, consultaListas, 20000)).optJSONArray(clave);
            for (int i = 0; datos != null && i < datos.length(); i++) lista.add(datos.getJSONObject(i));
        } catch (Exception sinLista) {
            lista.clear();
        }
        return lista;
    }

    private ReglasReproductor.Plan decidir(Integer audio) throws Exception {
        JSONObject cuerpo = new JSONObject();
        cuerpo.put("file_id", archivoId);
        cuerpo.put("player_id", presenciaId);
        JSONObject capacidades = recuperacion.flujoForzado() ? ReglasReproductor.capsFlujo(caps) : new JSONObject(caps.toString());
        if (recuperacion.videoForzado()) capacidades.put("source_video_unsupported", true);
        cuerpo.put("caps", capacidades);
        cuerpo.put("audio_index", audio == null ? JSONObject.NULL : audio);
        ReglasReproductor.Plan decision = new ReglasReproductor.Plan(new JSONObject(api.enviar("POST", "/api/v1/playback/decide", cuerpo.toString(), consultaPreparar)));
        // Passthrough-only codecs cannot be processed locally. Ask for the existing
        // audio-only fallback before opening, instead of failing mid-playback.
        if (!"off".equals(modoAudioGuardado(decision.dinamica)) && audioPCM != null && !audioPCM.toString().equals(caps.optJSONArray("audio").toString())) {
            caps.put("audio", audioPCM);
            capacidades.put("audio", audioPCM);
            cuerpo.put("caps", capacidades);
            decision = new ReglasReproductor.Plan(new JSONObject(api.enviar("POST", "/api/v1/playback/decide", cuerpo.toString(), consultaPreparar)));
        }
        return decision;
    }

    private void abrir(ReglasReproductor.Plan p, long posicionMs) {
        apertura++;
        plan = p;
        volumenAutomatico.modo(modoAudioGuardado(p.dinamica));
        audioActual = p.audio;
        audioPorAplicar = p.flujo() ? -1 : p.audio;
        if (p.flujo()) {
            abrirFlujo(posicionMs);
            return;
        }
        offsetMs = 0;
        player.setMediaItem(MediaItem.fromUri(Uri.parse(api.url(p.url))), posicionMs);
        player.prepare();
    }

    /** En un flujo reempaquetado, saltar es pedir otro flujo. El servidor dice en qué punto real empezará, para que la barra y los subtítulos coincidan. */
    private void abrirFlujo(long posicionMs) {
        double segundo = Math.max(0, posicionMs) / 1000.0;
        int pista = plan.audio;
        boolean exacto = "transcode".equals(plan.metodo);
        int mia = ++apertura;
        // Solo cuenta el último salto: el anterior deja de esperar al servidor (y el servidor detiene su cálculo).
        if (consultaSalto != null) consultaSalto.cancelar();
        ApiVelora.Cancelable control = new ApiVelora.Cancelable();
        consultaSalto = control;
        enFondo(() -> {
            if (mia != apertura) return; // ya hay otro salto detrás: ni se pregunta
            long inicio = Math.round(segundo * 1000);
            try {
                String ruta = "/api/v1/stream/files/" + archivoId + "/seek?t=" + String.format(Locale.ROOT, "%.3f", segundo) + "&audio=" + pista;
                double real = exacto ? segundo : new JSONObject(api.get(ruta, control, 15000)).optDouble("start", segundo);
                if (real <= segundo + 0.01) inicio = Math.round(real * 1000);
            } catch (Exception seUsaElPuntoPedido) {
                // Se empieza en el segundo pedido (si se ha cancelado, el resultado se descarta abajo).
            }
            long origen = inicio;
            enPrincipal(() -> {
                if (mia != apertura || plan == null) return;
                offsetMs = origen;
                intento++;
                player.setMediaItem(MediaItem.fromUri(Uri.parse(api.url(ReglasReproductor.urlFlujo(plan.url, segundo, intento)))));
                player.prepare();
            });
        });
    }

    /** Posición en el archivo, sea cual sea el modo. */
    private long posicionMs() {
        if (objetivoMs >= 0) return objetivoMs;
        if (player == null) return 0;
        return offsetMs + player.getCurrentPosition();
    }

    private long duracionMs() {
        if (plan != null && plan.duracionMs > 0) return plan.duracionMs;
        long d = player == null ? C.TIME_UNSET : player.getDuration();
        return d == C.TIME_UNSET ? 0 : d;
    }

    private void saltarA(long ms) {
        objetivoMs = ReglasReproductor.limitarMs(ms, duracionMs());
        hilo.removeCallbacks(aplicarSalto);
        hilo.postDelayed(aplicarSalto, CONFIRMAR_SALTO_MS);
        refrescar();
    }

    private void aplicarSaltoPendiente() {
        if (objetivoMs < 0 || plan == null) return;
        long ms = objetivoMs;
        objetivoMs = -1;
        if (plan.flujo()) abrirFlujo(ms);
        else player.seekTo(ms);
    }

    private void alternar() {
        if (player == null) return;
        if (player.isPlaying()) player.pause();
        else player.play();
    }

    private void alTerminar() {
        if (terminado || plan == null) return;
        if (ReglasReproductor.finPrematuro(plan.flujo(), posicionMs(), duracionMs())) {
            recuperar(ReglasReproductor.Causa.TRANSPORTE, null);
            return;
        }
        guardarProgreso();
        if (!siguienteCancelado && !siguienteId.isEmpty() && siguienteEpisodio()) return;
        salir();
    }

    /** Abre el episodio siguiente en otro reproductor y cierra este. FORWARD_RESULT: quien abrió el primero recibe el
     * resultado del último, como si fuera uno solo. */
    private boolean siguienteEpisodio() {
        try {
            JSONObject datos = new JSONObject(getIntent().getStringExtra(DATOS));
            datos.put("item", siguienteId);
            datos.put("controles_ocultos", true);
            datos.put("file", "");
            datos.put("sub", ""); // Los identificadores de pistas pertenecen al archivo anterior.
            datos.put("inicio", false);
            datos.put("audio", -1); // las pistas del episodio siguiente pueden ir en otro orden: la suya por defecto
            Intent i = new Intent(this, ReproductorActivity.class);
            i.putExtras(getIntent());
            i.putExtra(DATOS, datos.toString());
            i.addFlags(Intent.FLAG_ACTIVITY_FORWARD_RESULT);
            startActivity(i);
            terminado = true;
            finish();
            return true;
        } catch (JSONException | RuntimeException e) {
            return false;
        }
    }

    private static int codigoHttp(Throwable error) {
        for (Throwable t = error; t != null; t = t.getCause()) {
            if (t instanceof HttpDataSource.InvalidResponseCodeException) return ((HttpDataSource.InvalidResponseCodeException) t).responseCode;
        }
        return 0;
    }

    /**
     * Recuperación acotada y según la causa: un corte o un servidor ocupado se reintentan en el mismo modo tras una espera
     * corta; un formato o un audio que el aparato no abre se piden reempaquetados; un vídeo que el aparato no decodifica
     * se explica en lugar de reempaquetarlo. Agotados los intentos, el reproductor web.
     */
    private void recuperar(ReglasReproductor.Causa causa, Format pista) {
        if (plan == null) {
            salirConFallo();
            return;
        }
        if (causa==ReglasReproductor.Causa.FORMATO && pista!=null && pista.sampleMimeType!=null && pista.sampleMimeType.startsWith("audio/")) {
            try {caps.put("source_audio_unsupported",true);} catch(JSONException imposible) { }
        }
        long pos = posicionMs();
        switch (recuperacion.alFallar(plan.flujo(), pos, causa)) {
            case PEDIR_VIDEO:
            case PEDIR_FLUJO:
                int mia = ++apertura;
                enFondo(() -> {
                    try {
                        ReglasReproductor.Plan p = decidir(audioActual >= 0 ? audioActual : null);
                        enPrincipal(() -> {
                            if (mia != apertura) return;
                            if (p.reproducible() && p.flujo()) abrir(p, pos);
                            else salirConFallo();
                        });
                    } catch (Exception e) {
                        enPrincipal(this::salirConFallo);
                    }
                });
                break;
            case REINTENTAR:
                int turno = ++apertura;
                hilo.postDelayed(() -> {
                    if (!activa() || plan == null || turno != apertura) return; // otro salto o apertura ha pasado delante
                    long desde = posicionMs();
                    if (plan.flujo()) {
                        abrirFlujo(Math.max(0, desde - 1000));
                    } else {
                        player.setMediaItem(MediaItem.fromUri(Uri.parse(api.url(plan.url))), desde);
                        player.prepare();
                    }
                }, ReglasReproductor.esperaMs(causa, recuperacion.reintentos()));
                break;
            case NO_COMPATIBLE:
                String detalle = pista == null ? "" : " (" + (pista.codecs != null ? pista.codecs : String.valueOf(pista.sampleMimeType))
                        + (pista.height > 0 ? ", " + pista.height + " líneas" : "") + ")";
                avisarYSalir("Este aparato no consigue decodificar el vídeo de este archivo" + detalle
                        + ". Prueba otra versión del título o reprodúcelo en otro aparato.");
                break;
            default:
                salirConFallo();
                break;
        }
    }

    private void enviarPresencia(boolean detener) {
        if (plan == null || player == null || archivoId == null || archivoId.isEmpty()) return;
        long pos = Math.max(0, posicionMs());
        String estado = detener || player.getPlaybackState() == Player.STATE_ENDED ? "stopped"
                : !player.getPlayWhenReady() ? "paused"
                : player.getPlaybackState() == Player.STATE_BUFFERING ? "buffering" : "playing";
        String archivo = archivoId;
        ultimoLatido = System.currentTimeMillis();
        enFondo(() -> {
            try {
                JSONObject cuerpo = new JSONObject();
                cuerpo.put("id", presenciaId); cuerpo.put("file_id", archivo);
                cuerpo.put("state", estado); cuerpo.put("position_ms", pos);
                api.enviar("POST", "/api/v1/playback/heartbeat", cuerpo.toString());
            } catch (ApiVelora.ErrorApi e) {
                if (!detener && e.estado == 429) enPrincipal(() -> avisarYSalir("Se ha alcanzado el límite de reproducciones simultáneas."));
                // Tras un rato fuera de la app (sin latidos) el perfil puede haberse bloqueado: a la web, a por el PIN.
                if (!detener && ReglasReproductor.perfilBloqueado(e.estado)) enPrincipal(this::salirBloqueado);
            }
            catch (Exception siguienteLatido) { }
        });
    }

    private void guardarProgreso() {
        long ms = posicionMs();
        if (terminado || esExtra || plan == null || ms <= 0) return;
        ultimoGuardado = System.currentTimeMillis();
        long duracion = plan.duracionMs;
        String ruta = "/api/v1/me/items/" + itemId + "/progress";
        enFondo(() -> {
            try {
                JSONObject cuerpo = new JSONObject();
                cuerpo.put("file_id", archivoId);
                cuerpo.put("position_ms", ms);
                cuerpo.put("duration_ms", duracion);
                cuerpo.put("client_time", ahoraIso());
                api.enviar("PUT", ruta, cuerpo.toString());
            } catch (Exception seGuardaraLaProxima) {
                // El siguiente guardado lo intentará otra vez.
            }
        });
    }

    private static String ahoraIso() {
        java.text.SimpleDateFormat f = new java.text.SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.ROOT);
        f.setTimeZone(java.util.TimeZone.getTimeZone("UTC"));
        return f.format(new java.util.Date());
    }

    private void refrescar() {
        if (player == null) return;
        long pos = posicionMs();
        long dur = duracionMs();
        tiempo.setText(ReglasReproductor.tiempo(pos) + " / " + ReglasReproductor.tiempo(dur));
        restante.setText(dur > 0 ? "−" + ReglasReproductor.tiempo(dur - pos) : "");
        barra.setProgress(dur > 0 ? (int) Math.min(1000, pos * 1000 / dur) : 0);
        String texto = subActual == null ? "" : ReglasReproductor.cueActiva(cues, pos);
        if (!texto.contentEquals(textoSub.getText())) textoSub.setText(texto);
        textoSub.setVisibility(texto.isEmpty() ? View.GONE : View.VISIBLE);
        actualizarEspecial(pos);
        if (presenciaVisible && System.currentTimeMillis() - ultimoLatido >= 5000) enviarPresencia(false);
        if (player.isPlaying() && System.currentTimeMillis() - ultimoGuardado >= GUARDAR_MS) guardarProgreso();
    }

    /** «Saltar intro» o «Saltar créditos», solo dentro de un capítulo marcado como tal. */
    private void actualizarEspecial(long pos) {
        JSONObject actual = null;
        boolean conCreditos = false;
        for (JSONObject c : capitulos) {
            String tipo = c.optString("kind");
            if (tipo.isEmpty() || "null".equals(tipo)) continue;
            if ("credits".equals(tipo)) conCreditos = true;
            if (pos >= c.optDouble("start") * 1000 && pos < c.optDouble("end") * 1000 - 1000) actual = c;
        }
        // E2: sin créditos marcados ni detectados, «Siguiente episodio» aparece a 30 s del final, como en la web.
        long dur = duracionMs();
        if (actual == null && !conCreditos && !siguienteId.isEmpty() && dur > ReglasReproductor.ANTES_DEL_FINAL_MS * 2
                && pos >= dur - ReglasReproductor.ANTES_DEL_FINAL_MS && pos < dur) {
            if (finalVirtual == null) {
                try {
                    finalVirtual = new JSONObject().put("kind", "credits").put("start", (dur - ReglasReproductor.ANTES_DEL_FINAL_MS) / 1000.0).put("end", dur / 1000.0 + 1);
                } catch (JSONException imposible) {
                    finalVirtual = null;
                }
            }
            actual = finalVirtual;
        }
        especial = actual;
        if (panel.getVisibility() == View.VISIBLE) siguienteLimite = -1;
        if (panel.getVisibility() != View.VISIBLE && actual != null && "credits".equals(actual.optString("kind")) && !siguienteId.isEmpty() && !siguienteCancelado) {
            if (siguienteLimite < 0) siguienteLimite = pos + Math.min(10000, Math.max(250, Math.min(dur, Math.round(actual.optDouble("end") * 1000)) - pos - 1250));
            if (pos >= siguienteLimite && player != null && player.isPlaying() && !terminado) pulsarSaltar();
        }
        actualizarBotonEspecial();
    }

    /** Preselecciona cada aviso al aparecer; conserva la elección del mando entre refrescos. */
    private void actualizarBotonEspecial() {
        boolean visible = especial != null && panel.getVisibility() != View.VISIBLE;
        if (!visible && (saltar.hasFocus() || cancelarSiguiente.hasFocus())) {
            if (enBotones) reproducirPausa.requestFocus();
            else raiz.requestFocus();
        }
        if (visible) { controlesSoloManual = true; ocultarPorAviso(); }
        saltar.setVisibility(visible ? View.VISIBLE : View.GONE);
        boolean conSiguiente = visible && !"intro".equals(especial.optString("kind")) && !siguienteId.isEmpty();
        cancelarSiguiente.setVisibility(conSiguiente && !siguienteCancelado ? View.VISIBLE : View.GONE);
        FrameLayout.LayoutParams posicionAvisos = (FrameLayout.LayoutParams) avisos.getLayoutParams();
        posicionAvisos.bottomMargin = controles.getVisibility() == View.VISIBLE ? controles.getHeight() + dp(12) : dp(24);
        avisos.setLayoutParams(posicionAvisos);
        FrameLayout.LayoutParams posicionSub = (FrameLayout.LayoutParams) textoSub.getLayoutParams();
        posicionSub.bottomMargin = visible ? posicionAvisos.bottomMargin + dp(52)
                : controles.getVisibility() == View.VISIBLE ? controles.getHeight() + dp(12) : dp(28);
        textoSub.setLayoutParams(posicionSub);
        if (visible && (ultimoAviso != especial || (!saltar.hasFocus() && !cancelarSiguiente.hasFocus()))) {
            ultimoAviso = especial;
            saltar.requestFocus();
        }
        if (especial == null) ultimoAviso = null;
        if (especial != null) {
            saltar.setText("intro".equals(especial.optString("kind")) ? "Saltar intro"
                    : siguienteId.isEmpty() ? "Saltar créditos" : "Siguiente episodio" + (siguienteCancelado || siguienteLimite < 0 ? "" : " (" + Math.max(0, (siguienteLimite - posicionMs() + 999) / 1000) + " s)"));
        }
    }

    private void pulsarSaltar() {
        JSONObject c = especial;
        if (c == null) return;
        controlesSoloManual = true;
        ocultarPorAviso();
        if ("intro".equals(c.optString("kind"))) {
            saltarA(Math.round(c.optDouble("end") * 1000));
            return;
        }
        if (esExtra) {
            salir();
            return;
        }
        // Créditos: se da por vista y se vuelve a la ficha o, en un episodio con siguiente, se pasa a él.
        terminado = true;
        enFondo(() -> {
            try {
                api.enviar("POST", "/api/v1/me/items/" + itemId + "/played", null);
            } catch (Exception seQuedaSinMarcar) {
                // Se puede marcar a mano desde la ficha.
            }
            enPrincipal(() -> {
                if (siguienteId.isEmpty() || !siguienteEpisodio()) salirSinGuardar();
            });
        });
    }

    // Mando

    @Override
    public boolean dispatchKeyEvent(KeyEvent e) {
        int k = e.getKeyCode();
        if (k == KeyEvent.KEYCODE_BACK) return super.dispatchKeyEvent(e);
        boolean abajo = e.getAction() == KeyEvent.ACTION_DOWN;
        boolean aceptar = k == KeyEvent.KEYCODE_DPAD_CENTER || k == KeyEvent.KEYCODE_ENTER
                || k == KeyEvent.KEYCODE_NUMPAD_ENTER;
        // La acción puede ocultar el botón en ACTION_DOWN: consumir también su ACTION_UP.
        if (aceptar && activandoEspecial) {
            if (!abajo) activandoEspecial = false;
            return true;
        }
        if (abajo && especial == null && e.getRepeatCount() == 0) controlesSoloManual = false;
        switch (k) {
            case KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE:
            case KeyEvent.KEYCODE_HEADSETHOOK:
                if (abajo && e.getRepeatCount() == 0) alternar();
                return true;
            case KeyEvent.KEYCODE_MEDIA_PLAY:
                if (abajo && player != null) player.play();
                return true;
            case KeyEvent.KEYCODE_MEDIA_PAUSE:
                if (abajo && player != null) player.pause();
                return true;
            case KeyEvent.KEYCODE_MEDIA_REWIND:
                if (abajo) saltarConAviso(-ATRAS_MS);
                return true;
            case KeyEvent.KEYCODE_MEDIA_FAST_FORWARD:
                if (abajo) saltarConAviso(ADELANTE_MS);
                return true;
            case KeyEvent.KEYCODE_MEDIA_STOP:
                if (abajo) salir();
                return true;
            default:
                break;
        }
        // Con el panel abierto o sobre los botones, las flechas y OK las gestiona Android: es su foco normal.
        if (panel.getVisibility() == View.VISIBLE) return super.dispatchKeyEvent(e);
        if (saltar.hasFocus() || cancelarSiguiente.hasFocus()) {
            switch (k) {
                case KeyEvent.KEYCODE_DPAD_CENTER:
                case KeyEvent.KEYCODE_ENTER:
                case KeyEvent.KEYCODE_NUMPAD_ENTER:
                    if (abajo && e.getRepeatCount() == 0) {
                        activandoEspecial = true;
                        (cancelarSiguiente.hasFocus() ? cancelarSiguiente : saltar).performClick();
                    }
                    return true;
                case KeyEvent.KEYCODE_DPAD_UP:
                case KeyEvent.KEYCODE_DPAD_DOWN:
                    if (abajo && e.getRepeatCount() == 0) entrarEnBotones();
                    return true;
                case KeyEvent.KEYCODE_DPAD_LEFT:
                case KeyEvent.KEYCODE_DPAD_RIGHT:
                    if (cancelarSiguiente.getVisibility() == View.VISIBLE) {
                        if (abajo) (k == KeyEvent.KEYCODE_DPAD_LEFT ? cancelarSiguiente : saltar).requestFocus();
                        return true;
                    }
                    if (abajo) {
                        raiz.requestFocus();
                        enBotones = false;
                        botones.setVisibility(View.GONE);
                        saltarConAviso(k == KeyEvent.KEYCODE_DPAD_LEFT ? -ATRAS_MS : ADELANTE_MS);
                    }
                    return true;
                default:
                    return super.dispatchKeyEvent(e);
            }
        }
        if (enBotones) {
            if (k == KeyEvent.KEYCODE_DPAD_UP) {
                if (abajo) salirDeBotones();
                return true;
            }
            mostrarControles();
            return super.dispatchKeyEvent(e);
        }
        switch (k) {
            case KeyEvent.KEYCODE_DPAD_CENTER:
            case KeyEvent.KEYCODE_ENTER:
            case KeyEvent.KEYCODE_NUMPAD_ENTER:
                if (abajo && e.getRepeatCount() == 0) {
                    alternar();
                    mostrarControles();
                }
                return true;
            case KeyEvent.KEYCODE_DPAD_LEFT:
                if (abajo) saltarConAviso(-ATRAS_MS);
                return true;
            case KeyEvent.KEYCODE_DPAD_RIGHT:
                if (abajo) saltarConAviso(ADELANTE_MS);
                return true;
            case KeyEvent.KEYCODE_DPAD_UP:
            case KeyEvent.KEYCODE_DPAD_DOWN:
                if (abajo && e.getRepeatCount() == 0) {
                    if (saltar.getVisibility() == View.VISIBLE) saltar.requestFocus();
                    else entrarEnBotones();
                }
                return true;
            default:
                return super.dispatchKeyEvent(e);
        }
    }

    private void saltarConAviso(long deltaMs) {
        saltarA(posicionMs() + deltaMs);
        mostrarControles();
    }

    private void entrarEnBotones() {
        if (especial != null && panel.getVisibility() != View.VISIBLE) { saltar.requestFocus(); return; }
        enBotones = true;
        mostrarControles();
        botones.setVisibility(View.VISIBLE);
        reproducirPausa.requestFocus();
    }

    private void salirDeBotones() {
        enBotones = false;
        botones.setVisibility(View.GONE);
        raiz.requestFocus();
        programarOcultar();
    }

    @Override
    public void onBackPressed() {
        if (panel != null && panel.getVisibility() == View.VISIBLE) {
            cerrarPanel();
        } else if (saltar != null && (saltar.hasFocus() || cancelarSiguiente.hasFocus())) {
            raiz.requestFocus();
        } else if (enBotones) {
            salirDeBotones();
        } else {
            salir();
        }
    }

    private void ocultarPorAviso() {
        hilo.removeCallbacks(ocultar);
        enBotones = false;
        botones.setVisibility(View.GONE);
        controles.setVisibility(View.GONE);
        cabecera.setVisibility(View.GONE);
    }

    private void mostrarControles() {
        if ((especial != null || controlesSoloManual) && panel.getVisibility() != View.VISIBLE) { ocultarPorAviso(); return; }
        controles.setVisibility(View.VISIBLE);
        cabecera.setVisibility(View.VISIBLE);
        programarOcultar();
    }

    private void programarOcultar() {
        hilo.removeCallbacks(ocultar);
        hilo.postDelayed(ocultar, OCULTAR_MS);
    }

    private void ocultarControles() {
        if (player == null || !player.isPlaying() || panel.getVisibility() == View.VISIBLE) return;
        enBotones = false;
        botones.setVisibility(View.GONE);
        controles.setVisibility(View.GONE);
        cabecera.setVisibility(View.GONE);
        if (!saltar.hasFocus() && !cancelarSiguiente.hasFocus()) raiz.requestFocus();
    }

    // Panel de audio y subtítulos

    private String modoAudioGuardado(String defecto) {
        return getSharedPreferences("velora",MODE_PRIVATE).getString("audio_dynamics",defecto);
    }
    private void cambiarModoAudio(String modo) {
        if(!"off".equals(modo)&&!audioPCMActivo){
            String codec="";
            for(JSONObject a:audios)if(a.optInt("index",-1)==audioActual)codec=a.optString("codec");
            boolean decodificable=false;
            for(int i=0;audioPCM!=null&&i<audioPCM.length();i++)if(codec.equals(audioPCM.optString(i)))decodificable=true;
            if(!decodificable){avisar("Este audio solo puede enviarse al receptor en Original. El ajuste no se ha cambiado.");return;}
        }
        volumenAutomatico.modo(modo);
        getSharedPreferences("velora",MODE_PRIVATE).edit().putString("audio_dynamics",modo).apply();
        if(!audioPCMActivo&&!"off".equals(modo)){
            // Reconfigure only the audio renderer. Never replace the media item,
            // seek, pause video, or reopen its stream to change volume dynamics.
            int turno=++revisionModoAudio;
            player.setTrackSelectionParameters(player.getTrackSelectionParameters().buildUpon().setTrackTypeDisabled(C.TRACK_TYPE_AUDIO,true).build());
            hilo.postDelayed(()->{if(activa()&&turno==revisionModoAudio)player.setTrackSelectionParameters(player.getTrackSelectionParameters().buildUpon().setTrackTypeDisabled(C.TRACK_TYPE_AUDIO,false).build());},80);
        }
    }

    private void abrirPanel() {
        LinearLayout lista = (LinearLayout) panel.getChildAt(0);
        lista.removeAllViews();
        View elegida = null;
        lista.addView(encabezado("Audio"));
        lista.addView(encabezado("Volumen automático"));
        String modo=volumenAutomatico.modo();
        lista.addView(opcion("Original", "off".equals(modo),true,()->cambiarModoAudio("off")));
        lista.addView(opcion("Equilibrado · diálogos y acción", "balanced".equals(modo),true,()->cambiarModoAudio("balanced")));
        lista.addView(opcion("Nocturno · menos sonidos fuertes", "night".equals(modo),true,()->cambiarModoAudio("night")));
        if(audioPCMActivo&&"off".equals(modo))lista.addView(encabezado("Para enviar Atmos intacto, inicia la reproducción en Original."));
        if (audios.isEmpty()) lista.addView(encabezado("Sin pistas de audio"));
        for (JSONObject a : audios) {
            int indice = a.optInt("index", -1);
            boolean actual = indice == audioActual;
            View v = opcion(ReglasReproductor.etiquetaAudio(a.optString("language", ""), a.optString("codec", ""), a.optInt("channels"), a.optString("title", "")), actual, true, () -> elegirAudio(indice));
            lista.addView(v);
            if (actual) elegida = v;
        }
        lista.addView(encabezado("Subtítulos"));
        View sinSub = opcion("Desactivados", subActual == null, true, () -> elegirSubtitulo(null));
        lista.addView(sinSub);
        if (elegida == null) elegida = sinSub;
        int empotrado = 0;
        for (JSONObject s : subtitulos) {
            boolean externo = s.optBoolean("external");
            int orden = externo ? -1 : empotrado++;
            boolean texto = s.optBoolean("supported");
            // Un subtítulo de imagen solo lo puede dibujar el reproductor, y solo si el archivo le llega tal cual.
            boolean imagen = !texto && !externo && plan != null && !plan.flujo();
            String id = s.optString("id");
            String nombre = s.optString("label", ReglasReproductor.idioma(s.optString("language", "")) + (s.optBoolean("forced") ? " · Forzados" : " · Completos"));
            if (!s.optString("provider").isEmpty()) nombre += " · " + s.optString("title");
            lista.addView(opcion(texto || imagen ? nombre : nombre + " (no disponible en este modo)", id.equals(subActual), texto || imagen, () -> {
                if (texto) elegirSubtitulo(s);
                else elegirSubtituloDeImagen(id, orden);
            }));
        }
        panel.setVisibility(View.VISIBLE);
        actualizarBotonEspecial();
        controles.setVisibility(View.VISIBLE);
        cabecera.setVisibility(View.VISIBLE);
        hilo.removeCallbacks(ocultar);
        elegida.requestFocus();
    }

    private void cerrarPanel() {
        panel.setVisibility(View.GONE);
        actualizarBotonEspecial();
        if (enBotones) entrarEnBotones();
        else raiz.requestFocus();
        programarOcultar();
    }

    private void elegirAudio(int indice) {
        if(indice!=audioActual) caps.remove("source_audio_unsupported");
        audioActual = indice;
        int orden = -1;
        for (int i = 0; i < audios.size(); i++) {
            if (audios.get(i).optInt("index", -2) == indice) orden = i;
        }
        // Si el archivo llega tal cual, la pista ya está dentro y solo hay que cambiar a ella: es instantáneo.
        if (plan != null && !plan.flujo() && orden >= 0) {
            if (seleccionarGrupo(C.TRACK_TYPE_AUDIO, orden)) return;
            try {caps.put("source_audio_unsupported",true);caps.put("force_remux",true);} catch(JSONException imposible) { }
        }
        long pos = posicionMs();
        int mia = ++apertura;
        enFondo(() -> {
            try {
                ReglasReproductor.Plan p = decidir(indice);
                enPrincipal(() -> {
                    if (mia == apertura && p.reproducible()) abrir(p, pos);
                });
            } catch (Exception seQuedaLaActual) {
                enPrincipal(() -> avisar("No se ha podido cambiar el audio."));
            }
        });
    }

    /** El orden de las pistas de un tipo es el mismo en el servidor y en el reproductor: el del archivo. */
    private boolean seleccionarGrupo(int tipo, int orden) {
        int n = 0;
        for (Tracks.Group g : player.getCurrentTracks().getGroups()) {
            if (g.getType() != tipo) continue;
            if (n++ != orden) continue;
            if (!g.isTrackSupported(0)) return false;
            player.setTrackSelectionParameters(player.getTrackSelectionParameters().buildUpon()
                    .setTrackTypeDisabled(tipo, false)
                    .setOverrideForType(new TrackSelectionOverride(g.getMediaTrackGroup(), 0))
                    .build());
            return true;
        }
        return false;
    }

    private void apagarSubtitulosDelReproductor() {
        player.setTrackSelectionParameters(player.getTrackSelectionParameters().buildUpon().setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true).build());
    }

    private void elegirSubtitulo(JSONObject s) {
        if (cargaSubtitulos != null) cargaSubtitulos.cancelar();
        cargaSubtitulos = new ApiVelora.Cancelable();
        final ApiVelora.Cancelable carga = cargaSubtitulos;
        subtituloPedido="off";
        cues = Collections.emptyList();
        apagarSubtitulosDelReproductor();
        if (s == null) {
            subActual = null;
            return;
        }
        String id = s.optString("id");
        subActual = id;
        enAuxiliar(() -> {
            try {
                api.getProgresivo("/api/v1/stream/files/" + archivoId + "/subtitles/" + id, carga, texto -> {
                    List<ReglasReproductor.Cue> leidas = ReglasReproductor.leerVtt(texto);
                    enPrincipal(() -> { if (carga == cargaSubtitulos && id.equals(subActual)) cues = leidas; });
                });
            } catch (Exception e) {
                enPrincipal(() -> { if (carga == cargaSubtitulos && !carga.cancelada()) avisar("No se ha podido cargar este subtítulo."); });
            }
        });
    }

    private void elegirSubtituloDeImagen(String id, int orden) {
        if (cargaSubtitulos != null) cargaSubtitulos.cancelar();
        subtituloPedido="off";
        cues = Collections.emptyList();
        if (seleccionarGrupo(C.TRACK_TYPE_TEXT, orden)) subActual = id;
        else avisar("Este aparato no puede mostrar ese subtítulo.");
    }

    // Salida

    private void salir() {
        if (terminado) return;
        guardarProgreso();
        salirSinGuardar();
    }

    private void salirSinGuardar() {
        terminado = true;
        setResult(RESULT_OK);
        finish();
    }

    /**
     * El perfil está bloqueado (fase 4 de replica/PLAN_HOGARES_Y_PIN.md). El progreso ya se guardó al salir de la app;
     * ahora el servidor no aceptaría nada hasta que se escriba el PIN en la web.
     */
    private void salirBloqueado() {
        if (terminado) return;
        terminado = true;
        if (player != null) player.pause();
        setResult(RESULTADO_BLOQUEADO);
        finish();
    }

    /** Lo que Media3 no ha podido abrir lo intenta el reproductor web. */
    private void salirConFallo() {
        if (terminado) return;
        terminado = true;
        Intent r = new Intent();
        r.putExtra(ITEM, itemId);
        r.putExtra(ARCHIVO, archivoId);
        setResult(RESULTADO_FALLO, r);
        finish();
    }

    private void avisarYSalir(String texto) {
        if (isFinishing() || isDestroyed()) return;
        new AlertDialog.Builder(this)
                .setTitle("No se puede reproducir")
                .setMessage(texto)
                .setPositiveButton("Volver", (d, w) -> salirSinGuardar())
                .setOnCancelListener(d -> salirSinGuardar())
                .show();
    }

    private void avisar(String texto) {
        android.widget.Toast.makeText(getApplicationContext(), texto, android.widget.Toast.LENGTH_LONG).show();
    }

    @Override
    protected void onResume() {
        super.onResume();
        presenciaVisible = true;
        ultimoLatido = 0;
    }

    @Override
    protected void onPause() {
        presenciaVisible = false;
        enviarPresencia(true);
        super.onPause();
        if (player != null) {
            guardarProgreso();
            player.pause();
        }
    }

    @Override
    protected void onDestroy() {
        hilo.removeCallbacksAndMessages(null);
        if (player != null) {
            player.release();
            player = null;
        }
        consultaListas.cancelar();
        if (cargaSubtitulos != null) cargaSubtitulos.cancelar();
        consultaPreparar.cancelar();
        if (consultaSalto != null) consultaSalto.cancelar();
        imagenes.cerrar();
        auxiliar.shutdownNow();
        fondo.shutdown(); // Deja terminar el último guardado.
        super.onDestroy();
    }

    // Interfaz

    private int dp(int v) {
        return Math.round(TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v, getResources().getDisplayMetrics()));
    }

    private void construirInterfaz() {
        raiz = new FrameLayout(this);
        raiz.setBackgroundColor(Color.BLACK);
        raiz.setFocusable(true);
        raiz.setFocusableInTouchMode(true);

        logo = new ImageView(this);
        logo.setScaleType(ImageView.ScaleType.FIT_START);
        logo.setContentDescription("Logo del contenido");
        cabecera = new LinearLayout(this);
        cabecera.setOrientation(LinearLayout.VERTICAL);
        cabecera.setPadding(0, 0, 0, dp(8));
        cabecera.addView(logo, new LinearLayout.LayoutParams(dp(200), dp(72)));
        nombreSerie = textoPeque();
        nombreSerie.setTextSize(18);
        nombreSerie.setTypeface(Typeface.DEFAULT_BOLD);
        cabecera.addView(nombreSerie);
        numeroEpisodio = textoPeque();
        numeroEpisodio.setTypeface(Typeface.DEFAULT_BOLD);
        cabecera.addView(numeroEpisodio);
        nombreEpisodio = textoPeque();
        nombreEpisodio.setMaxLines(2);
        cabecera.addView(nombreEpisodio);
        for (TextView texto : new TextView[] { nombreSerie, numeroEpisodio, nombreEpisodio }) {
            texto.setShadowLayer(dp(3), 0, dp(1), Color.BLACK);
        }
        FrameLayout.LayoutParams marca = new FrameLayout.LayoutParams(dp(340), ViewGroup.LayoutParams.WRAP_CONTENT, Gravity.TOP | Gravity.START);
        marca.setMargins(dp(28), dp(24), dp(28), 0);
        raiz.addView(cabecera, marca);

        textoSub = new TextView(this);
        textoSub.setTextColor(Color.WHITE);
        textoSub.setTextSize(TypedValue.COMPLEX_UNIT_SP, 22);
        textoSub.setGravity(Gravity.CENTER);
        textoSub.setShadowLayer(6, 0, 2, Color.BLACK);
        textoSub.setVisibility(View.GONE);
        FrameLayout.LayoutParams sub = new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT, Gravity.BOTTOM);
        sub.setMargins(dp(48), 0, dp(48), dp(28));
        raiz.addView(textoSub, sub);

        cargando = new ProgressBar(this);
        cargando.setIndeterminateTintList(ColorStateList.valueOf(AZUL));
        raiz.addView(cargando, new FrameLayout.LayoutParams(dp(56), dp(56), Gravity.CENTER));

        controles = new LinearLayout(this);
        controles.setOrientation(LinearLayout.VERTICAL);
        controles.setGravity(Gravity.BOTTOM);
        controles.setPadding(dp(32), dp(20), dp(32), dp(20));
        controles.setBackground(new GradientDrawable(GradientDrawable.Orientation.BOTTOM_TOP, new int[] {Color.parseColor("#E6000000"), Color.TRANSPARENT}));

        titulo = new TextView(this);
        titulo.setTextColor(Color.WHITE);
        titulo.setTextSize(TypedValue.COMPLEX_UNIT_SP, 18);
        titulo.setTypeface(Typeface.DEFAULT_BOLD);
        titulo.setSingleLine(true);
        controles.addView(titulo);

        barra = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        barra.setMax(1000);
        barra.setProgressTintList(ColorStateList.valueOf(AZUL));
        barra.setFocusable(false);
        LinearLayout.LayoutParams lb = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(6));
        lb.setMargins(0, dp(10), 0, dp(4));
        controles.addView(barra, lb);

        LinearLayout tiempos = new LinearLayout(this);
        tiempo = textoPeque();
        restante = textoPeque();
        tiempos.addView(tiempo, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        tiempos.addView(restante);
        controles.addView(tiempos);

        botones = new LinearLayout(this);
        botones.setGravity(Gravity.CENTER_VERTICAL);
        botones.setPadding(0, dp(10), 0, 0);
        botones.setVisibility(View.GONE);
        botones.addView(icono(R.drawable.ic_rotate_ccw, "Retroceder 10 segundos", () -> saltarConAviso(-ATRAS_MS)));
        reproducirPausa = icono(R.drawable.ic_pause_relleno, "Reproducir o pausar", this::alternar);
        botones.addView(reproducirPausa);
        botones.addView(icono(R.drawable.ic_rotate_cw, "Avanzar 30 segundos", () -> saltarConAviso(ADELANTE_MS)));
        botones.addView(boton("Audio y subtítulos", this::abrirPanel));
        saltar = boton("Saltar intro", this::pulsarSaltar);
        saltar.setFocusableInTouchMode(true);
        saltar.setVisibility(View.GONE);
        controles.addView(botones);

        raiz.addView(controles, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT, Gravity.BOTTOM));

        FrameLayout.LayoutParams aviso = new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, dp(44), Gravity.BOTTOM | Gravity.RIGHT);
        // Cerca del borde inferior; se eleva solo cuando la barra está visible.
        aviso.setMargins(dp(24), 0, dp(24), dp(24));
        StateListDrawable fondoAviso = new StateListDrawable();
        GradientDrawable focoAviso = new GradientDrawable();
        focoAviso.setColor(AZUL);
        focoAviso.setCornerRadius(dp(22));
        fondoAviso.addState(new int[] {android.R.attr.state_focused}, focoAviso);
        GradientDrawable normalAviso = new GradientDrawable();
        normalAviso.setColor(FONDO);
        normalAviso.setCornerRadius(dp(22));
        fondoAviso.addState(new int[] {}, normalAviso);
        saltar.setBackground(fondoAviso);
        avisos = new LinearLayout(this);
        avisos.setGravity(Gravity.CENTER_VERTICAL);
        cancelarSiguiente = boton("Cancelar avance automático", () -> {
            siguienteCancelado = true;
            saltar.requestFocus();
            actualizarBotonEspecial();
        });
        cancelarSiguiente.setFocusableInTouchMode(true);
        cancelarSiguiente.setVisibility(View.GONE);
        avisos.addView(cancelarSiguiente);
        avisos.addView(saltar);
        raiz.addView(avisos, aviso);

        panel = new ScrollView(this);
        panel.setBackgroundColor(FONDO);
        panel.setVisibility(View.GONE);
        LinearLayout lista = new LinearLayout(this);
        lista.setOrientation(LinearLayout.VERTICAL);
        lista.setPadding(dp(12), dp(12), dp(12), dp(12));
        panel.addView(lista);
        raiz.addView(panel, new FrameLayout.LayoutParams(dp(340), ViewGroup.LayoutParams.MATCH_PARENT, Gravity.END));

        setContentView(raiz);
        raiz.requestFocus();
    }

    private TextView textoPeque() {
        TextView t = new TextView(this);
        t.setTextColor(Color.parseColor("#D0D6E0"));
        t.setTextSize(TypedValue.COMPLEX_UNIT_SP, 13);
        return t;
    }

    /** Elegido o con el foco: en otro color, sin sombras. */
    private StateListDrawable fondoFoco(float radio) {
        GradientDrawable foco = new GradientDrawable();
        foco.setColor(AZUL);
        foco.setCornerRadius(radio);
        StateListDrawable s = new StateListDrawable();
        s.addState(new int[] {android.R.attr.state_focused}, foco);
        s.addState(new int[] {}, new ColorDrawable(Color.TRANSPARENT));
        return s;
    }

    private ColorStateList colorTexto(int normal) {
        return new ColorStateList(new int[][] {{android.R.attr.state_focused}, {}}, new int[] {SOBRE_AZUL, normal});
    }

    private ImageButton icono(int imagen, String nombre, Runnable accion) {
        ImageButton b = new ImageButton(this);
        b.setImageResource(imagen);
        b.setContentDescription(nombre);
        b.setBackground(fondoFoco(dp(22)));
        b.setImageTintList(colorTexto(Color.WHITE));
        b.setPadding(dp(10), dp(10), dp(10), dp(10));
        b.setFocusable(true);
        b.setOnClickListener(v -> accion.run());
        b.setOnFocusChangeListener((v, tiene) -> programarOcultar());
        LinearLayout.LayoutParams l = new LinearLayout.LayoutParams(dp(44), dp(44));
        l.setMarginEnd(dp(8));
        b.setLayoutParams(l);
        return b;
    }

    private TextView boton(String texto, Runnable accion) {
        TextView b = new TextView(this);
        b.setText(texto);
        b.setTextSize(TypedValue.COMPLEX_UNIT_SP, 15);
        b.setTextColor(colorTexto(Color.WHITE));
        b.setGravity(Gravity.CENTER);
        b.setPadding(dp(16), 0, dp(16), 0);
        b.setBackground(fondoFoco(dp(22)));
        b.setFocusable(true);
        b.setClickable(true);
        b.setOnClickListener(v -> accion.run());
        LinearLayout.LayoutParams l = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(44));
        l.setMarginEnd(dp(8));
        b.setLayoutParams(l);
        return b;
    }

    private TextView encabezado(String texto) {
        TextView t = new TextView(this);
        t.setText(texto);
        t.setTextColor(Color.parseColor("#A9B2C0"));
        t.setTextSize(TypedValue.COMPLEX_UNIT_SP, 14);
        t.setTypeface(Typeface.DEFAULT_BOLD);
        t.setPadding(dp(8), dp(12), dp(8), dp(6));
        return t;
    }

    private TextView opcion(String texto, boolean actual, boolean disponible, Runnable accion) {
        TextView t = new TextView(this);
        t.setText((actual ? "✓  " : "    ") + texto);
        t.setTextSize(TypedValue.COMPLEX_UNIT_SP, 15);
        t.setTextColor(disponible ? colorTexto(actual ? AZUL : Color.WHITE) : ColorStateList.valueOf(Color.parseColor("#7A8291")));
        t.setPadding(dp(8), dp(10), dp(8), dp(10));
        t.setBackground(fondoFoco(dp(8)));
        t.setFocusable(disponible);
        t.setClickable(disponible);
        if (disponible) {
            t.setOnClickListener(v -> {
                accion.run();
                cerrarPanel();
            });
        }
        return t;
    }
}
