package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ResultCache guarda resultados de análisis caros (capítulos, origen de un salto) para no volver a abrir el archivo ni
// lanzar FFmpeg cuando se repite la misma consulta. Está acotada: al llenarse se descarta lo más antiguo. Quien la usa
// incluye en la clave el archivo y su versión (cambia al reanalizarlo), así que un archivo sustituido no reutiliza nada.
// Solo se guardan resultados buenos: un fallo se vuelve a intentar la próxima vez.
type ResultCache[V any] struct {
	mu    sync.Mutex
	max   int
	m     map[string]V
	order []string
}

// NewResultCache crea una caché de, como mucho, max resultados.
func NewResultCache[V any](max int) *ResultCache[V] {
	return &ResultCache[V]{max: max, m: map[string]V{}}
}

// Get devuelve el resultado guardado para la clave.
func (c *ResultCache[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[key]
	return v, ok
}

// Put guarda un resultado bueno.
func (c *ResultCache[V]) Put(key string, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[key]; !ok {
		c.order = append(c.order, key)
	}
	c.m[key] = v
	for len(c.order) > c.max {
		delete(c.m, c.order[0])
		c.order = c.order[1:]
	}
}

// Len cuenta los resultados guardados (pruebas).
func (c *ResultCache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// Limiter limita los procesos FFmpeg simultáneos (gobernador mínimo de E1; no es un límite de usuarios).
type Limiter struct{ slots chan struct{} }

// NewLimiter crea un limitador con n huecos.
func NewLimiter(n int) *Limiter {
	if n < 1 {
		n = 1
	}
	return &Limiter{slots: make(chan struct{}, n)}
}

// ErrBusy indica que el servidor ya tiene el máximo de conversiones en marcha.
var ErrBusy = errors.New("servidor ocupado")

// TryAcquire reserva un hueco sin esperar.
func (l *Limiter) TryAcquire() bool {
	select {
	case l.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// Acquire espera un hueco como mucho wait (o hasta que se cancele ctx).
func (l *Limiter) Acquire(ctx context.Context, wait time.Duration) bool {
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case l.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	case <-t.C:
		return false
	}
}

// Release libera el hueco.
func (l *Limiter) Release() { <-l.slots }

// InUse devuelve cuántos huecos están ocupados.
func (l *Limiter) InUse() int { return len(l.slots) }

// RemuxArgs construye los argumentos de FFmpeg para Direct Stream: vídeo copiado tal cual, audio copiado o convertido a
// AAC estéreo, salida en MP4 fragmentado por la salida estándar. La entrada es siempre /dev/fd/3.
func RemuxArgs(plan Plan, videoCodec string, startSec float64) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if startSec > 0 {
		// -noaccurate_seek: el audio empieza en el mismo punto que el vídeo copiado (el fotograma clave en el que cae
		// FFmpeg), no en el segundo pedido. Sin él, el audio convertido empezaba en t y el vídeo segundos antes, y los
		// dos se colocaban en 0: el audio iba adelantado hasta un GOP entero (o más, con fotogramas B).
		args = append(args, "-noaccurate_seek", "-ss", strconv.FormatFloat(startSec, 'f', 3, 64))
	}
	args = append(args, "-i", "/dev/fd/3", "-map", "0:v:0")
	if plan.AudioIndex >= 0 {
		args = append(args, "-map", "0:"+strconv.Itoa(plan.AudioIndex))
	}
	args = append(args, "-c:v", "copy")
	if videoCodec == "hevc" && plan.OutputContainer != "mkv" && plan.OutputContainer != "webm" {
		args = append(args, "-tag:v", "hvc1") // necesario para que Safari reconozca HEVC en MP4
	}
	if plan.AudioIndex >= 0 {
		if plan.ConvertAudio {
			if plan.OutputContainer == "webm" {
				args = append(args, "-c:a", "libopus", "-ac", "2", "-b:a", "128k", "-threads", "1")
			} else {
				args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "192k", "-threads", "1")
			}
		} else {
			args = append(args, "-c:a", "copy")
		}
	}
	if plan.OutputContainer == "mkv" {
		return append(args, "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1", "-avoid_negative_ts", "make_zero", "-cluster_time_limit", "1000", "-f", "matroska", "pipe:1")
	}
	if plan.OutputContainer == "webm" {
		return append(args, "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1", "-avoid_negative_ts", "make_zero", "-cluster_time_limit", "1000", "-f", "webm", "pipe:1")
	}
	// make_zero: el paquete más temprano pasa a ser el 0 del flujo, sea cual sea el punto; SeekOrigin calcula ese
	// instante con los mismos parámetros.
	return append(args, "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1", "-avoid_negative_ts", "make_zero",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof+delay_moov", "-f", "mp4", "pipe:1")
}

// remuxNice es la prioridad (nice) de FFmpeg en Direct Stream.
const remuxNice = 10

// IdleWriter corta un flujo cuando el cliente deja de leer: cada escritura amplía el plazo de escritura de la conexión;
// si el cliente (p. ej. en pausa) no lee en Idle, la escritura falla, FFmpeg recibe SIGPIPE y se libera su hueco.
type IdleWriter struct {
	W    io.Writer
	RC   interface{ SetWriteDeadline(time.Time) error }
	Idle time.Duration
}

// Abort corta en el acto cualquier escritura pendiente (se usa al cancelar el flujo: no se espera al cliente).
func (w IdleWriter) Abort() { _ = w.RC.SetWriteDeadline(time.Now().Add(-time.Second)) }

func (w IdleWriter) Write(p []byte) (int, error) {
	_ = w.RC.SetWriteDeadline(time.Now().Add(w.Idle))
	n, err := w.W.Write(p)
	if f, ok := w.W.(interface{ Flush() }); ok && err == nil {
		f.Flush()
	}
	return n, err
}

// Remux ejecuta FFmpeg y copia el MP4 fragmentado a w. Se detiene (matando el grupo de procesos) cuando se cancela ctx
// (el cliente cierra la conexión, architecture.md §5.1.2) o cuando falla la escritura (el cliente dejó de leer).
func Remux(ctx context.Context, f *os.File, args []string, w io.Writer) error {
	cmd := exec.Command("ffmpeg", args...)
	cmd.ExtraFiles = []*os.File{f}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr TailLog
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Prioridad baja: el remux no debe quitar CPU a los demás servicios del N100 (Jellyfin, Plex…).
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, remuxNice)
	// La copia es nuestra: si el cliente deja de leer o se va, se mata FFmpeg en vez de dejarlo bloqueado en la tubería.
	copied := make(chan error, 1)
	go func() {
		_, err := io.Copy(w, stdout)
		copied <- err
	}()
	stop := func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		t := time.AfterFunc(5*time.Second, func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
		<-copied // al morir FFmpeg se cierra la tubería y la copia termina
		_ = cmd.Wait()
		t.Stop()
	}
	select {
	case err := <-copied:
		if err != nil { // el cliente dejó de leer (corte por inactividad) o cerró la conexión
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
			return nil
		}
		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			return fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil
	case <-ctx.Done():
		if a, ok := w.(interface{ Abort() }); ok {
			a.Abort() // una escritura bloqueada hacia un cliente que ya se fue no retrasa el cierre
		}
		stop()
		return nil
	}
}

// SeekOrigin devuelve el instante del archivo que corresponde al segundo 0 del flujo optimizado pedido con -ss t (ver
// RemuxArgs). No se estima: se ejecuta FFmpeg con el mismo salto y las mismas pistas, copiando sin convertir y con las
// marcas de tiempo originales (-copyts), y se toma la marca de decodificación (dts) más temprana: es la que
// -avoid_negative_ts make_zero lleva a 0 (con fotogramas B, el vídeo se presenta un poco después de su dts). Así el reloj, la barra y los subtítulos del
// reproductor coinciden con la imagen aunque FFmpeg caiga en un fotograma clave anterior al esperado (pasa con
// fotogramas B: pidiendo 20,04 s puede empezar en 10,01 s).
func SeekOrigin(ctx context.Context, f *os.File, t float64, audioIndex int) (float64, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return t, err
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if t > 0 {
		args = append(args, "-noaccurate_seek", "-ss", strconv.FormatFloat(t, 'f', 3, 64))
	}
	args = append(args, "-copyts", "-i", "/dev/fd/3", "-map", "0:v:0")
	if audioIndex >= 0 {
		args = append(args, "-map", "0:"+strconv.Itoa(audioIndex))
	}
	// Unos pocos fotogramas bastan: el audio de ese tramo va intercalado delante o junto a ellos.
	args = append(args, "-c", "copy", "-frames:v", "8", "-f", "framecrc", "-")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.ExtraFiles = []*os.File{f}
	out, err := cmd.Output()
	if err != nil {
		return t, err
	}
	return earliestDTS(string(out), t)
}

// earliestDTS lee la salida de framecrc («#tb 0: 1/1000» y líneas «flujo, dts, pts, duración, tamaño, crc») y devuelve
// la dts más temprana en segundos; si no hay paquetes, fallback.
func earliestDTS(framecrc string, fallback float64) (float64, error) {
	tb := map[string]float64{}
	best, found := 0.0, false
	for _, line := range strings.Split(framecrc, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#tb ") {
			// «#tb 0: 1/1000»
			parts := strings.Fields(strings.TrimPrefix(line, "#tb "))
			if len(parts) == 2 {
				var num, den float64
				if _, err := fmt.Sscanf(parts[1], "%g/%g", &num, &den); err == nil && den > 0 {
					tb[strings.TrimSuffix(parts[0], ":")] = num / den
				}
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			continue
		}
		unit, ok := tb[strings.TrimSpace(fields[0])]
		if !ok {
			continue
		}
		dts, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if err != nil {
			continue
		}
		if sec := dts * unit; !found || sec < best {
			best, found = sec, true
		}
	}
	if !found {
		return fallback, fmt.Errorf("FFmpeg no devolvió paquetes tras el salto")
	}
	return best, nil
}
