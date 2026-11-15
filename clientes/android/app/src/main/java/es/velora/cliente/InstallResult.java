package es.velora.cliente;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageInstaller;
import android.widget.Toast;

/** Respuesta del sistema al instalar: pide la confirmación del usuario o informa de lo ocurrido. */
public final class InstallResult extends BroadcastReceiver {
    @Override
    public void onReceive(Context context, Intent intent) {
        DiagnosticoOta.anotarRespuesta(context, intent);
        int status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE);
        if (status == PackageInstaller.STATUS_PENDING_USER_ACTION) {
            Intent confirm = intent.getParcelableExtra(Intent.EXTRA_INTENT);
            if (confirm != null) {
                confirm.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                context.startActivity(confirm);
            }
        } else if (status != PackageInstaller.STATUS_SUCCESS && status != PackageInstaller.STATUS_FAILURE_ABORTED) {
            Toast.makeText(context, "No se ha podido instalar la actualización. Velora sigue igual.", Toast.LENGTH_LONG).show();
        }
    }
}
