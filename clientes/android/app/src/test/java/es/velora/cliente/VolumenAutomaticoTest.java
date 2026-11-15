package es.velora.cliente;

import static org.junit.Assert.*;
import androidx.media3.common.C;
import androidx.media3.common.audio.AudioProcessor.AudioFormat;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;
import org.junit.Test;

public class VolumenAutomaticoTest {
    private VolumenAutomatico processor(String mode, int channels) throws Exception {
        VolumenAutomatico p = new VolumenAutomatico(); p.modo(mode);
        AudioFormat f = new AudioFormat(48000, channels, C.ENCODING_PCM_16BIT);
        assertEquals(f, p.configure(f)); p.flush(); return p;
    }
    private double tone(VolumenAutomatico p, double amplitude, int channels) {
        ByteBuffer b = ByteBuffer.allocateDirect(48000 * channels * 2).order(ByteOrder.nativeOrder());
        for (int i=0; i<48000; i++) for(int c=0;c<channels;c++) b.putShort((short)(32767 * amplitude * Math.sin(i * 2 * Math.PI * 440 / 48000)));
        b.flip(); p.queueInput(b); ByteBuffer out=p.getOutput(); double squares=0; int n=0;
        out.position(out.limit()/2);
        while(out.hasRemaining()) {double v=out.getShort()/32768.0; assertTrue(Math.abs(v)<=0.951); squares+=v*v;n++;}
        return Math.sqrt(squares/n);
    }
    @Test public void narrowsDialogueActionDifferenceWithoutClipping() throws Exception {
        double quiet=tone(processor("balanced",2),0.04,2), loud=tone(processor("balanced",2),0.9,2);
        assertTrue("dialogue boosted",quiet>0.04/Math.sqrt(2));
        assertTrue("action/dialogue contrast reduced",loud/quiet<12);
        double nightQuiet=tone(processor("night",2),0.04,2), nightLoud=tone(processor("night",2),0.9,2);
        assertTrue(nightLoud/nightQuiet<loud/quiet);
    }
    @Test public void preservesChannelsTimingSilenceAndLinkedBalance() throws Exception {
        VolumenAutomatico p=processor("balanced",6);
        ByteBuffer b=ByteBuffer.allocateDirect(4800*12).order(ByteOrder.nativeOrder());
        for(int i=0;i<4800;i++) for(int c=0;c<6;c++) b.putShort((short)(c==0?30000:c==1?15000:0));
        b.flip();p.queueInput(b);ByteBuffer out=p.getOutput();assertEquals(4800*12,out.remaining());
        while(out.hasRemaining()) {int left=out.getShort(),right=out.getShort();assertTrue(Math.abs(left-2*right)<=1);for(int c=2;c<6;c++)assertEquals(0,out.getShort());}
        assertEquals(0,tone(processor("night",6),0,6),0);
    }
    @Test public void originalBypassesAndFlushResetsEnvelope() throws Exception {
        VolumenAutomatico p=new VolumenAutomatico();
        AudioFormat f=new AudioFormat(48000,2,C.ENCODING_PCM_16BIT);
        assertEquals(f,p.configure(f));p.flush();
        ByteBuffer raw=ByteBuffer.allocateDirect(4).order(ByteOrder.nativeOrder()).putShort((short)2345).putShort((short)-3456);raw.flip();p.queueInput(raw);ByteBuffer original=p.getOutput();assertEquals(2345,original.getShort());assertEquals(-3456,original.getShort());
        // A mode change uses the already configured PCM path, without flush or seek.
        p.modo("balanced");assertTrue(p.habilitado());assertTrue(tone(p,0.04,2)>0.04/Math.sqrt(2));
        p=processor("balanced",2);tone(p,0.9,2);p.flush();
        assertEquals(tone(processor("balanced",2),0.04,2),tone(p,0.04,2),0.000001);
    }
}
