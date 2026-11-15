package es.velora.cliente;

import androidx.media3.common.C;
import androidx.media3.common.audio.BaseAudioProcessor;
import androidx.media3.common.util.UnstableApi;
import java.nio.ByteBuffer;

/** Linked-channel gentle compression and peak protection on decoded PCM, without
 * re-encoding, changing sample rate or mixing channels. Off keeps original passthrough. */
@UnstableApi
final class VolumenAutomatico extends BaseAudioProcessor {
    private volatile String modo = "off";
    private double envolvente, limitador = 1;
    private double ataque, salida, recuperacion;

    void modo(String valor) { modo = "night".equals(valor) ? "night" : "balanced".equals(valor) ? "balanced" : "off"; }
    String modo() { return modo; }
    boolean habilitado() { return !"off".equals(modo); }

    @Override protected AudioFormat onConfigure(AudioFormat f) throws UnhandledAudioFormatException {
        if (f.encoding != C.ENCODING_PCM_16BIT) throw new UnhandledAudioFormatException(f);
        return f;
    }

    @Override protected void onFlush() {
        envolvente = 0;
        limitador = 1;
        int rate = Math.max(1, inputAudioFormat.sampleRate);
        ataque = Math.exp(-1.0 / (rate * 0.010));
        salida = Math.exp(-1.0 / (rate * 0.500));
        recuperacion = Math.exp(-1.0 / (rate * 0.080));
    }

    @Override public void queueInput(ByteBuffer input) {
        ByteBuffer output = replaceOutputBuffer(input.remaining());
        if (!habilitado()) { output.put(input).flip(); return; }
        int canales = inputAudioFormat.channelCount;
        boolean noche = "night".equals(modo);
        double umbral = noche ? -32 : -24, ratio = noche ? 4 : 2.2;
        double realce = noche ? 7 : 4;
        while (input.remaining() >= canales * 2) {
            int inicio = input.position();
            double pico = 0;
            for (int c = 0; c < canales; c++) pico = Math.max(pico, Math.abs(input.getShort(inicio + c * 2) / 32768.0));
            double suavizado = pico > envolvente ? ataque : salida;
            envolvente = suavizado * envolvente + (1 - suavizado) * pico;
            double db = 20 * Math.log10(Math.max(1e-10, envolvente));
            double sobre = db - umbral;
            // 12 dB soft knee; unity below it. No upward AGC: silence stays silent.
            double reduccion = sobre < -6 ? 0 : sobre > 6 ? (1 - 1 / ratio) * sobre : (1 - 1 / ratio) * (sobre + 6) * (sobre + 6) / 24;
            double ganancia = Math.pow(10, (realce - reduccion) / 20);
            double seguro = pico * ganancia > 0.95 ? 0.95 / (pico * ganancia) : 1;
            limitador = seguro < limitador ? seguro : recuperacion * limitador + (1 - recuperacion) * seguro;
            ganancia *= limitador;
            for (int c = 0; c < canales; c++) output.putShort((short)Math.max(-32768, Math.min(32767, Math.round(input.getShort() * ganancia))));
        }
        output.flip();
    }
}
