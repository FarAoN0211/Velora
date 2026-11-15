package es.velora.cliente;

import android.content.Context;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;

/**
 * Una pantalla nativa defectuosa no debe dejar el aparato sin actualizaciones: cada arranque se apunta como cierre antes de construir
 * nada y se borra si la pantalla aguanta. Tras dos cierres seguidos se abre la web, que no depende de ella y sigue buscando el arreglo.
 */
final class GuardianTv {
    private static final String CIERRES = "tv_cierres";
    private static final String VERSION = "tv_cierres_version";

    private GuardianTv() {}

    static boolean nativaPermitida(Context c) {
        SharedPreferences p = preferencias(c);
        return ReglasTv.nativaPermitida(p.getInt(CIERRES, 0), p.getInt(VERSION, 0), version(c));
    }

    static void arranque(Context c) {
        SharedPreferences p = preferencias(c);
        int actual = version(c);
        int cierres = p.getInt(VERSION, 0) == actual ? p.getInt(CIERRES, 0) : 0;
        // Tiene que quedar escrito ya: si se aplazara, un cierre inmediato lo perdería.
        p.edit().putInt(VERSION, actual).putInt(CIERRES, cierres + 1).commit();
    }

    static void estable(Context c) {
        preferencias(c).edit().putInt(CIERRES, 0).apply();
    }

    private static SharedPreferences preferencias(Context c) {
        return c.getSharedPreferences("velora", Context.MODE_PRIVATE);
    }

    private static int version(Context c) {
        try {
            return c.getPackageManager().getPackageInfo(c.getPackageName(), 0).versionCode;
        } catch (PackageManager.NameNotFoundException imposible) {
            return 0;
        }
    }
}
