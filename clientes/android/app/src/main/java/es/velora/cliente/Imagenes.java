package es.velora.cliente;

import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.os.Handler;
import android.os.Looper;
import android.util.LruCache;
import android.widget.ImageView;
import java.io.*;
import java.lang.ref.WeakReference;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.*;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** Miniaturas con sesión, solicitudes compartidas y caché privada acotada. */
final class Imagenes {
    private static final int BYTES_MAXIMOS = 10 * 1024 * 1024;
    private final Handler hilo = new Handler(Looper.getMainLooper());
    private final ExecutorService fondo = Executors.newFixedThreadPool(4);
    private final LruCache<String, Bitmap> cache;
    private final Map<String, List<WeakReference<ImageView>>> pendientes = new HashMap<>();
    private String origen = "";
    private Map<String, String> cabeceras;
    private int generacion;

    Imagenes() {
        int kb = (int) Math.min(Runtime.getRuntime().maxMemory() / 1024 / 6, 32 * 1024);
        cache = new LruCache<String, Bitmap>(kb) {
            @Override protected int sizeOf(String clave, Bitmap b) { return Math.max(1, b.getByteCount() / 1024); }
        };
    }
    void sesion(String origen, Map<String, String> cabeceras) {
        if (!this.origen.equals(origen) || !Objects.equals(this.cabeceras, cabeceras)) {
            generacion++; cache.evictAll(); pendientes.clear();
        }
        this.origen = origen; this.cabeceras = new HashMap<>(cabeceras);
    }
    void poner(ImageView vista, String ruta, int ancho, int alto) {
        if (fondo.isShutdown()) return;
        if (ruta == null) { vista.setImageDrawable(null); vista.setTag(R.id.imagen_ruta, null); return; }
        boolean externa = ruta.startsWith("https://image.tmdb.org/t/p/w185/");
        int tam = ancho <= 160 ? 160 : ancho <= 320 ? 320 : ancho <= 640 ? 640 : 1280;
        String reducida = externa ? ruta : ruta + (ruta.contains("?") ? "&" : "?") + "w=" + tam;
        final String clave = generacion + ":" + reducida + "@" + ancho + "x" + alto;
        if (clave.equals(vista.getTag(R.id.imagen_ruta))) return;
        vista.setTag(R.id.imagen_ruta, clave);
        Bitmap ya = cache.get(clave);
        if (ya != null) { vista.setImageBitmap(ya); return; }
        vista.setImageDrawable(null);
        List<WeakReference<ImageView>> espera = pendientes.get(clave);
        if (espera != null) { espera.add(new WeakReference<>(vista)); return; }
        espera = new ArrayList<>(); espera.add(new WeakReference<>(vista)); pendientes.put(clave, espera);
        final int sesion = generacion;
        final String url = externa ? reducida : origen + reducida;
        final Map<String,String> headers = externa ? null : new HashMap<>(cabeceras);
        // Cache identity includes the authenticated session, so users never share disk entries.
        final String identidad = origen + (cabeceras == null ? "" : cabeceras.get("Cookie"));
        final File dir = new File(vista.getContext().getCacheDir(), "imagenes-velora");
        final boolean persistir = externa || ruta.contains("v=");
        fondo.execute(() -> {
            Bitmap b = null;
            File archivo = new File(dir, hash(identidad + url));
            try {
                byte[] datos = null;
                if (persistir && archivo.isFile() && archivo.length() <= BYTES_MAXIMOS) {
                    try (InputStream in = new FileInputStream(archivo)) { datos = leer(in); }
                }
                if (datos != null) b = decodificar(datos, ancho, alto);
                if (b == null) {
                    datos = descargar(url, headers);
                    b = decodificar(datos, ancho, alto);
                    if (b != null && persistir && (dir.isDirectory() || dir.mkdirs())) {
                        File tmp = File.createTempFile("imagen-", ".tmp", dir);
                        try { try (OutputStream out = new FileOutputStream(tmp)) { out.write(datos); } tmp.renameTo(archivo); } finally { tmp.delete(); }
                        recortar(dir);
                    }
                }
            } catch (IOException | RuntimeException | OutOfMemoryError sinImagen) { /* El vídeo no depende de una imagen. */ }
            final Bitmap imagen = b;
            hilo.post(() -> {
                if (sesion != generacion) return;
                List<WeakReference<ImageView>> vistas = pendientes.remove(clave);
                if (imagen != null) cache.put(clave, imagen);
                if (vistas != null) for (WeakReference<ImageView> ref : vistas) {
                    ImageView v = ref.get();
                    if (v != null && clave.equals(v.getTag(R.id.imagen_ruta))) {
                        if (imagen != null) v.setImageBitmap(imagen);
                        else v.setTag(R.id.imagen_ruta, null); // Allow retry on the next bind.
                    }
                }
            });
        });
    }
    void cerrar() { generacion++; fondo.shutdownNow(); pendientes.clear(); cache.evictAll(); }
    private static String hash(String s) {
        try {
            byte[] bytes = MessageDigest.getInstance("SHA-256").digest(s.getBytes(StandardCharsets.UTF_8));
            StringBuilder out = new StringBuilder(); for (byte b : bytes) out.append(String.format(Locale.ROOT, "%02x", b & 255)); return out.toString();
        } catch (Exception e) { throw new IllegalStateException(e); }
    }
    private static synchronized void recortar(File dir) {
        File[] files = dir.listFiles((d,n) -> !n.endsWith(".tmp")); if (files == null) return;
        Arrays.sort(files, Comparator.comparingLong(File::lastModified));
        long total = 0; for (File f : files) total += f.length();
        int cantidad = files.length;
        for (File f : files) { if (total <= 32L*1024*1024 && cantidad <= 128) break; long size = f.length(); if (f.delete()) { total -= size; cantidad--; } }
    }
    private static Bitmap decodificar(byte[] d, int ancho, int alto) {
        BitmapFactory.Options o = new BitmapFactory.Options(); o.inJustDecodeBounds = true;
        BitmapFactory.decodeByteArray(d, 0, d.length, o); o.inJustDecodeBounds = false;
        int r = 1; while (o.outWidth/(r*2) >= Math.max(1,ancho) && o.outHeight/(r*2) >= Math.max(1,alto)) r *= 2;
        o.inSampleSize = r; o.inPreferredConfig = Bitmap.Config.ARGB_8888; // Logos retain transparency.
        return BitmapFactory.decodeByteArray(d, 0, d.length, o);
    }
    private static byte[] leer(InputStream in) throws IOException {
        ByteArrayOutputStream out = new ByteArrayOutputStream(); byte[] b = new byte[16384]; int r;
        while ((r = in.read(b)) > 0) { out.write(b,0,r); if (out.size() > BYTES_MAXIMOS) throw new IOException("Imagen demasiado grande"); }
        return out.toByteArray();
    }
    private static byte[] descargar(String url, Map<String,String> headers) throws IOException {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        try {
            c.setConnectTimeout(8000); c.setReadTimeout(15000); c.setInstanceFollowRedirects(false);
            if (headers != null) for (Map.Entry<String,String> h : headers.entrySet()) c.setRequestProperty(h.getKey(),h.getValue());
            if (c.getResponseCode() != 200) throw new IOException("Sin imagen");
            try (InputStream in = c.getInputStream()) { return leer(in); }
        } finally { c.disconnect(); }
    }
}
