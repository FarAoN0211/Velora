package es.velora.cliente;

import android.content.Context;
import android.content.res.ColorStateList;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.ColorDrawable;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.GradientDrawable;
import android.graphics.drawable.StateListDrawable;
import android.text.TextUtils;
import android.view.Gravity;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

/** Las piezas de la interfaz de TV, con los colores de la web. Los títulos son solo texto: lo único que recibe el foco son carátulas y acciones. */
final class VistasTv {
    static final int FONDO = Color.parseColor("#0E1116");
    static final int LATERAL = Color.parseColor("#161A21");
    static final int ELEVADO = Color.parseColor("#1E232C");
    static final int TEXTO = Color.parseColor("#ECEFF4");
    static final int APAGADO = Color.parseColor("#A7B0BD");
    static final int ACENTO = Color.parseColor("#8FB0FF");
    static final int SOBRE_ACENTO = Color.parseColor("#0B1020");

    /** Medidas en dp pensadas para 960×540, lo que ve la app en una TV Box de 720p. */
    static final int ANCHO_LATERAL = 176;
    static final int ANCHO_CARATULA = 104;
    static final int ANCHO_PANORAMICA = 176;
    static final int HUECO = 12;

    private VistasTv() {}

    static int dp(Context c, float v) {
        return Math.round(v * c.getResources().getDisplayMetrics().density);
    }

    static TextView texto(Context c, String t, float sp, int color) {
        TextView v = new TextView(c);
        v.setText(t);
        v.setTextSize(sp);
        v.setTextColor(color);
        v.setIncludeFontPadding(false);
        v.setFocusable(false);
        return v;
    }

    static TextView titulo(Context c, String t, float sp) {
        TextView v = texto(c, t, sp, TEXTO);
        v.setTypeface(Typeface.DEFAULT_BOLD);
        return v;
    }

    /** Una TV Box puede arrancar en modo táctil, y ahí el foco solo lo admite lo marcado así: sin esto la primera pulsación del mando se perdería. */
    static void recibeFoco(View v) {
        v.setFocusable(true);
        v.setFocusableInTouchMode(true);
        v.setClickable(true);
    }

    private static Drawable estados(Context c, float radio, int normal, int foco) {
        StateListDrawable d = new StateListDrawable();
        d.addState(new int[] {android.R.attr.state_focused}, relleno(c, radio, foco));
        d.addState(new int[] {}, relleno(c, radio, normal));
        return d;
    }

    private static GradientDrawable relleno(Context c, float radio, int color) {
        GradientDrawable g = new GradientDrawable();
        g.setCornerRadius(dp(c, radio));
        g.setColor(color);
        return g;
    }

    /** Lo seleccionado cambia de color: ni sombra ni velo. */
    static TextView boton(Context c, String t) {
        TextView v = texto(c, t, 13, TEXTO);
        v.setTextColor(new ColorStateList(new int[][] {{android.R.attr.state_focused}, {}}, new int[] {SOBRE_ACENTO, TEXTO}));
        v.setBackground(estados(c, 6, ELEVADO, ACENTO));
        v.setGravity(Gravity.CENTER);
        v.setSingleLine(true);
        v.setMinHeight(dp(c, 32));
        v.setPadding(dp(c, 14), dp(c, 6), dp(c, 14), dp(c, 6));
        recibeFoco(v);
        return v;
    }

    /** Sección del lateral. Abierta: letra en color de acento. Con el foco: fondo de ese color. */
    static TextView seccion(Context c, String t, int icono) {
        TextView v = texto(c, t, 13, TEXTO);
        v.setTextColor(new ColorStateList(
                new int[][] {{android.R.attr.state_focused}, {android.R.attr.state_activated}, {}},
                new int[] {SOBRE_ACENTO, ACENTO, TEXTO}));
        v.setBackground(estados(c, 6, Color.TRANSPARENT, ACENTO));
        v.setSingleLine(true);
        v.setEllipsize(TextUtils.TruncateAt.END);
        v.setGravity(Gravity.CENTER_VERTICAL);
        v.setMinHeight(dp(c, 34));
        v.setPadding(dp(c, 10), dp(c, 6), dp(c, 10), dp(c, 6));
        v.setCompoundDrawablePadding(dp(c, 10));
        v.setCompoundDrawableTintList(v.getTextColors());
        Drawable d = c.getDrawable(icono);
        if (d != null) {
            d.setBounds(0, 0, dp(c, 18), dp(c, 18));
            v.setCompoundDrawablesRelative(d, null, null, null);
        }
        recibeFoco(v);
        return v;
    }

    /** Una carátula. El marco es lo único que se resalta y el título va debajo como texto. */
    static final class Carta extends LinearLayout {
        final ImageView imagen;
        private final FrameLayout marco;
        private final TextView nombre;
        private final TextView detalle;
        private final TextView sinImagen;
        private final View barra;
        private final View visto;
        private final int anchoImagen;
        private final int altoImagen;
        private final GradientDrawable borde;

        Carta(Context c, boolean panoramica) {
            super(c);
            setOrientation(VERTICAL);
            anchoImagen = dp(c, panoramica ? ANCHO_PANORAMICA : ANCHO_CARATULA);
            altoImagen = panoramica ? anchoImagen * 9 / 16 : anchoImagen * 3 / 2;
            setClipChildren(false);
            setClipToPadding(false);
            marco = new FrameLayout(c);
            int grosor = dp(c, 2);
            marco.setPadding(grosor, grosor, grosor, grosor);
            borde = relleno(c, 6, ELEVADO);
            marco.setBackground(borde);
            imagen = new ImageView(c);
            imagen.setScaleType(ImageView.ScaleType.CENTER_CROP);
            imagen.setClipToOutline(true);
            imagen.setBackground(relleno(c, 5, ELEVADO));
            marco.addView(imagen, new FrameLayout.LayoutParams(FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));
            sinImagen = texto(c, "", 12, APAGADO);
            sinImagen.setGravity(Gravity.CENTER);
            sinImagen.setPadding(grosor * 2, 0, grosor * 2, 0);
            marco.addView(sinImagen, new FrameLayout.LayoutParams(FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));
            barra = new View(c);
            barra.setBackgroundColor(ACENTO);
            marco.addView(barra, new FrameLayout.LayoutParams(0, dp(c, 3), Gravity.BOTTOM | Gravity.START));
            visto = new View(c);
            visto.setBackground(c.getDrawable(R.drawable.ic_check));
            FrameLayout.LayoutParams lv = new FrameLayout.LayoutParams(dp(c, 18), dp(c, 18), Gravity.TOP | Gravity.END);
            lv.setMargins(0, dp(c, 4), dp(c, 4), 0);
            marco.addView(visto, lv);
            addView(marco, new LayoutParams(anchoImagen, altoImagen));
            nombre = texto(c, "", 12, TEXTO);
            nombre.setSingleLine(true);
            nombre.setEllipsize(TextUtils.TruncateAt.END);
            nombre.setPadding(0, dp(c, 5), 0, 0);
            addView(nombre, new LayoutParams(anchoImagen, LayoutParams.WRAP_CONTENT));
            detalle = texto(c, "", 11, APAGADO);
            detalle.setSingleLine(true);
            detalle.setEllipsize(TextUtils.TruncateAt.END);
            detalle.setPadding(0, dp(c, 2), 0, 0);
            addView(detalle, new LayoutParams(anchoImagen, LayoutParams.WRAP_CONTENT));
        }

        void pintar(ReglasTv.Tarjeta t, Imagenes imagenes, boolean panoramica) {
            nombre.setText(t.titulo);
            detalle.setText(panoramica && t.reanudable() ? ReglasTv.restante(t) : t.anio > 0 ? String.valueOf(t.anio) : "");
            String ruta = panoramica ? t.imagenAncha() : t.poster ? ReglasTv.imagen(t.id, "poster", t.rev) : null;
            sinImagen.setText(ruta == null ? t.titulo : "");
            imagenes.poner(imagen, ruta, anchoImagen, altoImagen);
            float p = ReglasTv.progreso(t);
            FrameLayout.LayoutParams lb = (FrameLayout.LayoutParams) barra.getLayoutParams();
            lb.width = Math.round((anchoImagen - marco.getPaddingLeft() * 2) * p);
            barra.setLayoutParams(lb);
            barra.setVisibility(p > 0 ? VISIBLE : GONE);
            visto.setVisibility(t.vista ? VISIBLE : GONE);
        }

        void resaltar(boolean si) {
            borde.setColor(si ? ACENTO : ELEVADO);
            nombre.setTextColor(si ? ACENTO : TEXTO);
            animate().scaleX(si ? 1.06f : 1f).scaleY(si ? 1.06f : 1f).setDuration(120).start();
        }
    }

    static GradientDrawable degradado(GradientDrawable.Orientation sentido, int desde, int hasta) {
        return new GradientDrawable(sentido, new int[] {desde, hasta});
    }

    static ColorDrawable transparente() {
        return new ColorDrawable(Color.TRANSPARENT);
    }
}
