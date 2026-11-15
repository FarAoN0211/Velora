package es.velora.cliente;

import android.app.Activity;
import android.app.AlertDialog;
import android.app.PendingIntent;
import android.content.Intent;
import android.content.pm.PackageInfo;
import android.content.pm.PackageInstaller;
import android.content.pm.PackageManager;
import android.content.pm.Signature;
import android.net.Uri;
import android.os.Build;
import android.os.SystemClock;
import android.provider.Settings;
import android.widget.Toast;
import java.io.File;
import java.io.FileInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.HashSet;
import java.util.Set;

/**
 * Actualizaciones desde el propio servidor de Velora, por HTTP en la red local. Las reglas están en {@link ReglasOta}.
 *
 * Sin HTTPS, cualquiera en la red podría cambiar lo que se descarga. La protección es la firma: el APK debe llevar la misma firma que la
 * app instalada (lo comprueba esta clase y, sobre todo, Android, que rechaza cualquier otra). La huella solo detecta descargas dañadas,
 * porque viaja por el mismo canal. Tampoco se admite una versión igual o anterior.
 *
 * El turno se toma al comprobar y se suelta al terminar de verdad: sin novedad, «Más tarde», error o instalación entregada al sistema.
 */
final class Actualizador {
    private final Activity activity;
    private final ReglasOta.Plazo plazo = new ReglasOta.Plazo();
    private final ReglasOta.Turno turno = new ReglasOta.Turno();
    private AlertDialog dialogo;
    private AlertDialog dialogoDiagnostico;
    private File pendiente;
    private boolean enAjustes;

    Actualizador(Activity activity) {
        this.activity = activity;
    }

    void comprobar(String origen) {
        // Tiempo desde el arranque: a una TV Box recién encendida se le puede cambiar la hora del sistema.
        if (!plazo.toca(SystemClock.elapsedRealtime()) || !turno.tomar()) return;
        new Thread(() -> {
            limpiar(); // Restos de un intento anterior en el que se cerró la app.
            try {
                ReglasOta.Anuncio anuncio = ReglasOta.leerAnuncio(new String(leerAnuncio(origen + "/ota/velora.json"), "UTF-8"));
                plazo.acierto(SystemClock.elapsedRealtime());
                if (anuncio.esMasNuevoQue(actual())) {
                    activity.runOnUiThread(() -> ofrecer(origen, anuncio));
                    return;
                }
            } catch (Exception sinNovedad) {
                // Sin actualización publicada, sin red o respuesta extraña: la app sigue igual y no molesta.
                plazo.fallo(SystemClock.elapsedRealtime());
            }
            turno.soltar();
        }, "ota").start();
    }

    /** Vuelve de los ajustes del sistema: con el permiso concedido instala; sin él, lo deja para otra ocasión. */
    void continuar() {
        mostrarDiagnostico();
        if (pendiente == null) return;
        File apk = pendiente;
        if (puedeInstalar()) {
            pendiente = null;
            instalar(apk);
        } else if (enAjustes) {
            aviso("Sin ese permiso no se puede actualizar. Velora sigue igual y lo ofrecerá más tarde.");
            abandonar();
        }
    }

    /** La pantalla se cierra: sus avisos no pueden seguir abiertos. */
    void cerrar() {
        if (dialogo != null) dialogo.dismiss();
        if (dialogoDiagnostico != null) dialogoDiagnostico.dismiss();
    }

    private void mostrarDiagnostico() {
        AlertDialog d = DiagnosticoOta.mostrar(activity);
        if (d == null) return;
        if (dialogoDiagnostico != null) dialogoDiagnostico.dismiss();
        dialogoDiagnostico = d;
    }

    private boolean viva() {
        return !activity.isFinishing() && !activity.isDestroyed();
    }

    private void aviso(String texto) {
        Toast.makeText(activity.getApplicationContext(), texto, Toast.LENGTH_LONG).show();
    }

    private void abandonar() {
        pendiente = null;
        enAjustes = false;
        limpiar();
        plazo.fallo(SystemClock.elapsedRealtime());
        turno.soltar();
    }

    private File carpeta() {
        return new File(activity.getCacheDir(), "ota");
    }

    private void limpiar() {
        File[] restos = carpeta().listFiles();
        if (restos != null) for (File f : restos) f.delete();
    }

    private void ofrecer(String origen, ReglasOta.Anuncio anuncio) {
        if (!viva()) {
            turno.soltar();
            return;
        }
        boolean[] aceptada = {false};
        dialogo = new AlertDialog.Builder(activity)
                .setTitle("Hay una versión nueva de Velora" + (anuncio.nombre.isEmpty() ? "" : " (" + anuncio.nombre + ")"))
                .setMessage("Se descargará desde tu servidor. Tus datos y tu sesión no cambian.")
                .setPositiveButton("Actualizar (recomendado)", (d, w) -> {
                    aceptada[0] = true;
                    descargar(origen, anuncio);
                })
                .setNegativeButton("Más tarde", null)
                .setOnDismissListener(d -> {
                    dialogo = null;
                    if (!aceptada[0]) turno.soltar();
                })
                .show();
    }

    private void descargar(String origen, ReglasOta.Anuncio anuncio) {
        aviso("Descargando la actualización…");
        new Thread(() -> {
            try {
                File dir = carpeta();
                dir.mkdirs();
                File apk = new File(dir, "velora.apk");
                bajar(origen + "/ota/" + anuncio.archivo, apk, anuncio.huella);
                comprobarApk(apk, anuncio.version);
                activity.runOnUiThread(() -> {
                    if (!viva()) abandonar();
                    else if (puedeInstalar()) instalar(apk);
                    else pedirPermiso(apk);
                });
            } catch (Exception e) {
                String motivo = e instanceof ReglasOta.ErrorOta ? e.getMessage() : "fallo de conexión";
                activity.runOnUiThread(() -> {
                    aviso("No se ha podido actualizar: " + motivo + ". Velora sigue igual.");
                    abandonar();
                });
            }
        }, "ota-descarga").start();
    }

    private void comprobarApk(File apk, int version) throws IOException, PackageManager.NameNotFoundException {
        PackageManager pm = activity.getPackageManager();
        int flags = Build.VERSION.SDK_INT >= 28 ? PackageManager.GET_SIGNING_CERTIFICATES : PackageManager.GET_SIGNATURES;
        PackageInfo nueva = pm.getPackageArchiveInfo(apk.getPath(), flags);
        if (nueva == null) throw new ReglasOta.ErrorOta("el archivo no es Velora");
        String paquete = activity.getPackageName();
        ReglasOta.comprobarApk(paquete, nueva.packageName, version, nueva.versionCode, actual(), firmas(nueva), firmas(pm.getPackageInfo(paquete, flags)));
    }

    private static Set<String> firmas(PackageInfo info) throws IOException {
        Signature[] todas = Build.VERSION.SDK_INT >= 28
                ? (info.signingInfo == null ? null : info.signingInfo.getApkContentsSigners()) : info.signatures;
        Set<String> out = new HashSet<>();
        if (todas == null) return out;
        for (Signature s : todas) out.add(ReglasOta.hex(ReglasOta.sha256().digest(s.toByteArray())));
        return out;
    }

    private boolean puedeInstalar() {
        return Build.VERSION.SDK_INT < 26 || activity.getPackageManager().canRequestPackageInstalls();
    }

    private void pedirPermiso(File apk) {
        pendiente = apk;
        boolean[] aceptada = {false};
        dialogo = new AlertDialog.Builder(activity)
                .setTitle("Falta un permiso, solo esta vez")
                .setMessage("Android necesita que permitas a Velora instalar sus actualizaciones. Actívalo en la pantalla siguiente y vuelve.")
                .setPositiveButton("Abrir el permiso (recomendado)", (d, w) -> {
                    try {
                        activity.startActivity(new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:" + activity.getPackageName())));
                        aceptada[0] = true;
                        enAjustes = true;
                    } catch (RuntimeException sinPantalla) {
                        aviso("Este aparato no tiene esa pantalla: actívalo en Ajustes → Seguridad → Orígenes desconocidos.");
                    }
                })
                .setNegativeButton("Más tarde", null)
                .setOnDismissListener(d -> {
                    dialogo = null;
                    if (!aceptada[0]) abandonar();
                })
                .show();
    }

    /** La copia a la sesión del sistema va en segundo plano: con un almacenamiento lento bloquearía la pantalla. */
    private void instalar(File apk) {
        new Thread(() -> {
            boolean entregada = entregarAlSistema(apk);
            activity.runOnUiThread(() -> {
                if (!entregada) {
                    aviso("No se ha podido instalar. Velora sigue igual.");
                    abandonar();
                    mostrarDiagnostico();
                    return;
                }
                // El sistema ya tiene su copia y responde en InstallResult.
                enAjustes = false;
                limpiar();
                turno.soltar();
            });
        }, "ota-instalacion").start();
    }

    private boolean entregarAlSistema(File apk) {
        PackageInstaller instalador = activity.getPackageManager().getPackageInstaller();
        int id = -1;
        int intento = DiagnosticoOta.nuevoIntento(activity);
        String etapa = "crear la sesión";
        boolean confirmada = false;
        DiagnosticoOta.anotar(activity, intento, id, "Inicio: APK de " + apk.length() + " bytes", false);
        try {
            PackageInstaller.SessionParams params = new PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL);
            params.setSize(apk.length());
            id = instalador.createSession(params);
            etapa = "abrir la sesión y el archivo";
            try (PackageInstaller.Session sesion = instalador.openSession(id)) {
                // La copia se cierra una sola vez, aquí, antes de confirmar. Antes se cerraba dos veces (a mano y al salir del bloque):
                // el segundo cierre escribe en un descriptor ya cerrado y daba un fallo falso después de entregar el APK.
                try (InputStream in = new FileInputStream(apk); OutputStream out = sesion.openWrite("velora", 0, apk.length())) {
                    etapa = "copiar el APK";
                    byte[] b = new byte[65536];
                    int r;
                    while ((r = in.read(b)) > 0) out.write(b, 0, r);
                    etapa = "sincronizar la copia";
                    sesion.fsync(out);
                    etapa = "cerrar la copia";
                }
                etapa = "confirmar la sesión";
                Intent aviso = new Intent(activity, InstallResult.class).putExtra(DiagnosticoOta.EXTRA_INTENTO, intento);
                int flags = PendingIntent.FLAG_UPDATE_CURRENT | (Build.VERSION.SDK_INT >= 31 ? PendingIntent.FLAG_MUTABLE : 0);
                sesion.commit(PendingIntent.getBroadcast(activity, id, aviso, flags).getIntentSender());
                confirmada = true;
                etapa = "cerrar tras confirmar la sesión";
                DiagnosticoOta.anotar(activity, intento, id, "Sesión confirmada", false);
            }
            return true;
        } catch (IOException | RuntimeException e) {
            DiagnosticoOta.anotarFallo(activity, intento, id, etapa, e);
            // Ya confirmada, la instalación es del sistema: abandonarla la cancelaría, y él responde en InstallResult.
            if (confirmada) return true;
            // Una sesión a medias ocuparía espacio en el sistema hasta que este la descartara.
            if (id != -1) {
                try {
                    instalador.abandonSession(id);
                    DiagnosticoOta.anotar(activity, intento, id, "La app ha abandonado la sesión", false);
                } catch (RuntimeException yaCerrada) {
                    DiagnosticoOta.anotar(activity, intento, id, "No se ha podido abandonar la sesión: " + yaCerrada, false);
                }
            }
            return false;
        }
    }

    private int actual() throws PackageManager.NameNotFoundException {
        return activity.getPackageManager().getPackageInfo(activity.getPackageName(), 0).versionCode;
    }

    private static HttpURLConnection abrir(String url) throws IOException {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        c.setConnectTimeout(8000);
        c.setReadTimeout(30000);
        c.setInstanceFollowRedirects(false);
        if (c.getResponseCode() != 200) {
            c.disconnect();
            throw new IOException("el servidor respondió " + c.getResponseCode());
        }
        return c;
    }

    private static byte[] leerAnuncio(String url) throws IOException {
        HttpURLConnection c = abrir(url);
        try (InputStream in = c.getInputStream()) {
            return ReglasOta.leerAcotado(in, ReglasOta.MAX_ANUNCIO);
        } finally {
            c.disconnect();
        }
    }

    private static void bajar(String url, File destino, String huella) throws IOException {
        HttpURLConnection c = abrir(url);
        try (InputStream in = c.getInputStream()) {
            ReglasOta.guardar(in, c.getContentLength(), destino, ReglasOta.MAX_APK, huella);
        } finally {
            c.disconnect();
        }
    }
}
