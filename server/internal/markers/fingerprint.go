// Package markers detecta la intro y los créditos de los episodios comparando su audio (E2; architecture.md, VF-060).
//
// Método (huella de audio de Haitsma y Kalker, la base de Chromaprint): el audio se pasa a mono a 5512 Hz, se divide en
// tramos de 2048 muestras cada 256 (unos 46 ms) y en cada tramo se mide la energía de 33 bandas entre 300 y 2000 Hz.
// Cada bit de la huella dice si la diferencia entre dos bandas vecinas crece o decrece respecto al tramo anterior: es
// estable ante cambios de volumen y de compresión, y distinto en audios distintos. Dos episodios de una temporada
// comparten la intro (al principio) y los créditos (al final): se busca el tramo más largo en el que sus huellas
// coinciden. Sin dependencias nuevas: FFmpeg ya está en la imagen.
package markers

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/bits"
	"math/cmplx"
	"os"
	"os/exec"
	"strconv"
)

const (
	sampleRate = 5512
	frameSize  = 2048
	hopSize    = 256
	bands      = 33
	minFreq    = 300.0
	maxFreq    = 2000.0
)

// FrameSeconds es la duración de un paso de la huella.
const FrameSeconds = float64(hopSize) / sampleRate

// Fingerprint calcula la huella de [start, start+dur) segundos del primer audio del archivo f (abierto con os.Root:
// FFmpeg lo lee por /dev/fd/3, sin ruta). Un archivo sin audio da una huella vacía.
func Fingerprint(ctx context.Context, f *os.File, start, dur float64) ([]uint32, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin", "-ss", strconv.FormatFloat(start, 'f', 3, 64),
		"-t", strconv.FormatFloat(dur, 'f', 3, 64), "-i", "/dev/fd/3", "-map", "0:a:0?", "-vn", "-sn", "-dn",
		"-ac", "1", "-ar", strconv.Itoa(sampleRate), "-f", "s16le", "pipe:1")
	cmd.ExtraFiles = []*os.File{f}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	samples, readErr := readSamples(out, int(dur*sampleRate)+frameSize)
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	if readErr != nil {
		return nil, readErr
	}
	return FingerprintSamples(samples), nil
}

func readSamples(r io.Reader, capHint int) ([]float64, error) {
	buf := make([]byte, 64<<10)
	samples := make([]float64, 0, capHint)
	var rest []byte
	for {
		n, err := r.Read(buf)
		data := append(rest, buf[:n]...)
		for len(data) >= 2 {
			samples = append(samples, float64(int16(binary.LittleEndian.Uint16(data))))
			data = data[2:]
		}
		rest = append(rest[:0], data...)
		if err == io.EOF {
			return samples, nil
		}
		if err != nil {
			return samples, err
		}
	}
}

// FingerprintSamples calcula la huella de muestras mono a 5512 Hz.
func FingerprintSamples(samples []float64) []uint32 {
	if len(samples) < frameSize {
		return nil
	}
	window := make([]float64, frameSize)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(frameSize-1))
	}
	// Bordes de banda en escala logarítmica, en índices del espectro.
	edges := make([]int, bands+1)
	for b := 0; b <= bands; b++ {
		freq := minFreq * math.Pow(maxFreq/minFreq, float64(b)/bands)
		edges[b] = int(math.Round(freq * frameSize / sampleRate))
	}
	frames := (len(samples)-frameSize)/hopSize + 1
	energy := make([][bands]float64, frames)
	buf := make([]complex128, frameSize)
	for fr := 0; fr < frames; fr++ {
		off := fr * hopSize
		for i := 0; i < frameSize; i++ {
			buf[i] = complex(samples[off+i]*window[i], 0)
		}
		fft(buf)
		for b := 0; b < bands; b++ {
			var e float64
			for k := edges[b]; k < edges[b+1]; k++ {
				m := cmplx.Abs(buf[k])
				e += m * m
			}
			energy[fr][b] = e
		}
	}
	out := make([]uint32, 0, frames)
	for fr := 1; fr < frames; fr++ {
		var v uint32
		for b := 0; b < bands-1; b++ {
			d := (energy[fr][b] - energy[fr][b+1]) - (energy[fr-1][b] - energy[fr-1][b+1])
			if d > 0 {
				v |= 1 << uint(b)
			}
		}
		out = append(out, v)
	}
	return out
}

// fft es una FFT radix-2 en el sitio (len(a) potencia de 2).
func fft(a []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		step := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u := a[start+k]
				v := a[start+k+size/2] * w
				a[start+k] = u + v
				a[start+k+size/2] = u - v
				w *= step
			}
		}
	}
}

// Match es el tramo común más largo de dos huellas, en pasos de cada una.
type Match struct {
	AStart, BStart, Length int
}

const (
	matchWindow = 64 // ~3 s: la coincidencia se mide en ventanas, no paso a paso (un paso suelto puede fallar)
	// maxWindowErrors: media de bits distintos por paso en una ventana para darla por igual. Dos audios distintos difieren
	// en unos 16 de 32; el mismo audio recomprimido, en 2-6.
	maxWindowErrors = 9.0
)

// BestMatch busca el tramo común más largo entre a y b, en cualquier desplazamiento. minLen: longitud mínima en pasos.
func BestMatch(a, b []uint32, minLen int) (Match, bool) {
	best := Match{}
	if len(a) < matchWindow || len(b) < matchWindow {
		return best, false
	}
	limit := int(maxWindowErrors * matchWindow)
	dist := make([]int, 0, max(len(a), len(b)))
	// Cada desplazamiento d alinea a[i] con b[i-d].
	for d := -(len(b) - matchWindow); d <= len(a)-matchWindow; d++ {
		i0 := max(0, d)
		j0 := i0 - d
		n := min(len(a)-i0, len(b)-j0)
		if n < matchWindow || n < minLen {
			continue
		}
		dist = dist[:0]
		for k := 0; k < n; k++ {
			dist = append(dist, bits.OnesCount32(a[i0+k]^b[j0+k]))
		}
		sum := 0
		for k := 0; k < matchWindow; k++ {
			sum += dist[k]
		}
		runStart := -1
		for k := 0; k+matchWindow <= n; k++ {
			if k > 0 {
				sum += dist[k+matchWindow-1] - dist[k-1]
			}
			if sum <= limit {
				if runStart < 0 {
					runStart = k
				}
				continue
			}
			if runStart >= 0 {
				if st, l := trim(dist, runStart, k-1+matchWindow); l > best.Length {
					best = Match{AStart: i0 + st, BStart: j0 + st, Length: l}
				}
				runStart = -1
			}
		}
		if runStart >= 0 {
			if st, l := trim(dist, runStart, n); l > best.Length {
				best = Match{AStart: i0 + st, BStart: j0 + st, Length: l}
			}
		}
	}
	return best, best.Length >= minLen
}

// trim recorta los bordes de un tramo [start, end): una ventana que solo en parte cae dentro de la coincidencia ya puede
// pasar el umbral, así que el tramo sale algo más largo de lo que es. Se quitan los pasos de los bordes mientras la
// media de los 8 siguientes siga siendo la de dos audios distintos.
func trim(dist []int, start, end int) (int, int) {
	const edge, edgeMax = 8, 9 * 8
	sum := func(a int) int {
		t := 0
		for k := a; k < a+edge; k++ {
			t += dist[k]
		}
		return t
	}
	for end-start > 2*edge && sum(start) > edgeMax {
		start++
	}
	for end-start > 2*edge && sum(end-edge) > edgeMax {
		end--
	}
	return start, end - start
}
