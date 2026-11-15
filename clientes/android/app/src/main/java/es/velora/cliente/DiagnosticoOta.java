package es.velora.cliente;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageInstaller;
import android.util.Log;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.Locale;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

/**
 * Bitácora de cada intento de instalar una actualización, por intento y sesión y en orden: un fallo y su consecuencia (excepción, sesión abandonada,
 * respuesta del instalador) son entradas distintas y ninguna pisa a otra. Va en el registro de Android y a la vista del usuario, que puede fotografiarla.
 * No depende de nada más de la app: el mismo archivo sirve para instrumentar una versión antigua sin cambiar su lógica (replica/DIAGNOSTICO_OTA.md).
 */
final class DiagnosticoOta {
    static final String EXTRA_INTENTO = "velora_ota_intento";
    static final int MAX_ENTRADAS = 40;
    private static final String ETIQUETA = "VeloraOTA";
    private static final String BITACORA = "ota_bitacora";
    private static final String ORDEN = "ota_bitacora_orden";
    private static final String VISTO = "ota_bitacora_visto";
    private static final String INTENTO = "ota_bitacora_intento";

    private DiagnosticoOta() {}

    static synchronized int nuevoIntento(Context c) {
        SharedPreferences p = preferencias(c);
        int n = p.getInt(INTENTO, 0) + 1;
        p.edit().putInt(INTENTO, n).commit();
        return n;
    }

    static synchronized void anotar(Context c, int intento, int sesion, String texto, boolean fallo) {
        SharedPreferences p = preferencias(c);
        long orden = p.getLong(ORDEN, 0) + 1;
        Log.println(fallo ? Log.ERROR : Log.INFO, ETIQUETA, "intento " + intento + ", sesión " + sesion + ": " + texto);
        // commit y no apply: tras este paso el sistema puede sustituir la app y acabar con el proceso.
        p.edit().putLong(ORDEN, orden)
                .putString(BITACORA, anadir(p.getString(BITACORA, "[]"), orden, intento, sesion, System.currentTimeMillis(), texto, fallo))
                .commit();
    }

    static void anotarFallo(Context c, int intento, int sesion, String etapa, Throwable e) {
        Log.e(ETIQUETA, "Excepción en «" + etapa + "»", e);
        anotar(c, intento, sesion, describirFallo(etapa, e), true);
    }

    /** La respuesta del instalador del sistema, tal como llega a InstallResult. */
    static void anotarRespuesta(Context c, Intent i) {
        int estado = i.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE);
        anotar(c, i.getIntExtra(EXTRA_INTENTO, 0), i.getIntExtra(PackageInstaller.EXTRA_SESSION_ID, -1),
                describirEstado(estado, i.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE)),
                estado != PackageInstaller.STATUS_SUCCESS && estado != PackageInstaller.STATUS_PENDING_USER_ACTION);
    }

    /** Enseña la bitácora si hay un fallo que no se haya visto: una respuesta tardía la vuelve a enseñar, con todo lo anterior. */
    static synchronized AlertDialog mostrar(Activity a) {
        SharedPreferences p = preferencias(a);
        String json = p.getString(BITACORA, "[]");
        long ultimo = falloMasNuevo(json);
        if (ultimo <= p.getLong(VISTO, 0) || a.isFinishing() || a.isDestroyed()) return null;
        p.edit().putLong(VISTO, ultimo).commit();
        return new AlertDialog.Builder(a)
                .setTitle("Resultado de la última actualización")
                .setMessage(texto(json) + "\n\nVelora sigue funcionando. Si avisas del problema, indica este texto.")
                .setPositiveButton("Entendido", null)
                .show();
    }

    private static SharedPreferences preferencias(Context c) {
        return c.getSharedPreferences("velora", Context.MODE_PRIVATE);
    }

    static String describirFallo(String etapa, Throwable e) {
        return "Fallo en «" + etapa + "»: " + e.getClass().getSimpleName() + (e.getMessage() == null ? "" : ": " + e.getMessage());
    }

    /** Con los valores de PackageInstaller.STATUS_*. */
    static String describirEstado(int estado, String mensaje) {
        String nombre;
        switch (estado) {
            case -1: nombre = "pide confirmación al usuario"; break;
            case 0: nombre = "instalada"; break;
            case 1: nombre = "fallo general"; break;
            case 2: nombre = "bloqueada por el sistema"; break;
            case 3: nombre = "cancelada o abandonada"; break;
            case 4: nombre = "APK no válido"; break;
            case 5: nombre = "conflicto con la versión instalada"; break;
            case 6: nombre = "sin espacio"; break;
            case 7: nombre = "incompatible con este aparato"; break;
            default: nombre = "estado desconocido";
        }
        return "Respuesta del instalador: " + nombre + " (" + estado + ")" + (mensaje == null || mensaje.isEmpty() ? "" : ": " + mensaje);
    }

    static String anadir(String json, long orden, int intento, int sesion, long cuando, String texto, boolean fallo) {
        JSONArray antes = leer(json);
        JSONArray r = new JSONArray();
        try {
            for (int i = Math.max(0, antes.length() - MAX_ENTRADAS + 1); i < antes.length(); i++) r.put(antes.get(i));
            r.put(new JSONObject().put("orden", orden).put("intento", intento).put("sesion", sesion)
                    .put("cuando", cuando).put("texto", texto).put("fallo", fallo));
        } catch (JSONException imposible) {
            throw new IllegalStateException(imposible);
        }
        return r.toString();
    }

    static long falloMasNuevo(String json) {
        JSONArray a = leer(json);
        long ultimo = 0;
        for (int i = 0; i < a.length(); i++) {
            JSONObject e = a.optJSONObject(i);
            if (e != null && e.optBoolean("fallo")) ultimo = Math.max(ultimo, e.optLong("orden"));
        }
        return ultimo;
    }

    /** En orden de llegada. Solo el último intento con un fallo: los anteriores siguen en el registro de Android. */
    static String texto(String json) {
        JSONArray a = leer(json);
        int intento = -1;
        for (int i = 0; i < a.length(); i++) {
            JSONObject e = a.optJSONObject(i);
            if (e != null && e.optBoolean("fallo")) intento = e.optInt("intento");
        }
        SimpleDateFormat hora = new SimpleDateFormat("HH:mm:ss", Locale.ROOT);
        StringBuilder r = new StringBuilder();
        for (int i = 0; i < a.length(); i++) {
            JSONObject e = a.optJSONObject(i);
            if (e == null || e.optInt("intento") != intento) continue;
            if (r.length() > 0) r.append('\n');
            r.append(hora.format(new Date(e.optLong("cuando")))).append(" · intento ").append(e.optInt("intento"))
                    .append(", sesión ").append(e.optInt("sesion")).append(": ").append(e.optString("texto"));
        }
        return r.toString();
    }

    private static JSONArray leer(String json) {
        try {
            return new JSONArray(json);
        } catch (JSONException dañada) {
            return new JSONArray(); // Es solo diagnóstico: nunca debe estorbar a la actualización.
        }
    }
}
