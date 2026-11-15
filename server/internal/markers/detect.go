package markers

import (
	"context"
	"errors"
	"math"
	"os"
	"time"

	"github.com/google/uuid"
)

// Version del método: si cambia, los archivos ya analizados se vuelven a analizar.
const Version = 1

const (
	introWindow   = 600.0 // la intro se busca en los primeros 10 min (y como mucho en el 40 % del episodio)
	creditsWindow = 420.0 // los créditos, en los últimos 7 min (y como mucho en el 30 % final)
	minSegment    = 15.0  // tramos más cortos no se marcan: una coincidencia breve puede ser casualidad
	maxIntro      = 180.0 // una «intro» más larga es otra cosa (un resumen o un archivo repetido)
)

// Episode es un episodio de la temporada que se analiza, en orden.
type Episode struct {
	FileID   uuid.UUID
	Duration float64 // segundos
	Open     func() (*os.File, error)
	// Load y Store, opcionales: huellas ya calculadas de este archivo (leerlo de un montaje remoto lleva minutos).
	Load  func() (intro, credits []uint32, ok bool)
	Store func(intro, credits []uint32)
	// Skip: el archivo ya tiene su resultado; solo se usa de vecino y no aparece en la salida.
	Skip bool
}

// ErrStopped: se pidió parar entre dos episodios (fin de la tanda). Lo ya leído queda guardado con Store.
var ErrStopped = errors.New("detección detenida; continúa en la siguiente tanda")

// fingerprintTimeout es el plazo para leer un tramo de audio: un montaje colgado no retiene la tanda entera.
var fingerprintTimeout = 25 * time.Minute

// Segment es un tramo en segundos del archivo.
type Segment struct {
	Start, End float64
}

// Result es lo encontrado en un archivo; nil donde no se ha encontrado nada fiable.
type Result struct {
	Intro, Credits *Segment
}

type prints struct {
	intro, credits           []uint32
	introStart, creditsStart float64
	duration                 float64
	ok                       bool
}

// DetectSeason compara cada episodio con sus vecinos (anterior y siguiente, y si no basta, los de dos más allá) y
// devuelve, por archivo, la intro y los créditos que comparte con alguno de ellos. Los episodios que no se pueden leer
// se quedan sin resultado; no impiden analizar los demás.
func DetectSeason(ctx context.Context, eps []Episode) (map[uuid.UUID]Result, error) {
	return DetectSeasonUntil(ctx, eps, nil)
}

// DetectSeasonUntil es DetectSeason con una parada limpia: stop se consulta antes de leer cada archivo y, si dice que
// sí, se devuelve ErrStopped (las huellas ya calculadas quedan guardadas por Store y la siguiente llamada las reutiliza).
func DetectSeasonUntil(ctx context.Context, eps []Episode, stop func() bool) (map[uuid.UUID]Result, error) {
	fp := make([]prints, len(eps))
	for i, e := range eps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.Duration < 2*minSegment {
			continue
		}
		p := prints{duration: e.Duration}
		introDur := math.Min(introWindow, e.Duration*0.4)
		creditsDur := math.Min(creditsWindow, e.Duration*0.3)
		p.creditsStart = e.Duration - creditsDur
		if e.Load != nil {
			if in, cr, ok := e.Load(); ok {
				p.intro, p.credits, p.ok = in, cr, true
				fp[i] = p
				continue
			}
		}
		if stop != nil && stop() {
			return nil, ErrStopped
		}
		f, err := e.Open()
		if err != nil {
			continue
		}
		p.intro, err = fingerprintWithin(ctx, f, 0, introDur)
		if err == nil {
			p.credits, err = fingerprintWithin(ctx, f, p.creditsStart, creditsDur)
		}
		f.Close()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		if e.Store != nil {
			e.Store(p.intro, p.credits)
		}
		p.ok = true
		fp[i] = p
	}
	out := map[uuid.UUID]Result{}
	minLen := int(math.Ceil(minSegment / FrameSeconds))
	for i, e := range eps {
		if !fp[i].ok || e.Skip {
			continue
		}
		var r Result
		for _, d := range []int{-1, 1, -2, 2} {
			j := i + d
			if j < 0 || j >= len(eps) || !fp[j].ok || eps[j].FileID == e.FileID {
				continue
			}
			if m, ok := BestMatch(fp[i].intro, fp[j].intro, minLen); ok {
				s := segment(fp[i].introStart, m)
				if s.End-s.Start <= maxIntro && (r.Intro == nil || s.End-s.Start > r.Intro.End-r.Intro.Start) {
					r.Intro = &s
				}
			}
			if m, ok := BestMatch(fp[i].credits, fp[j].credits, minLen); ok {
				s := segment(fp[i].creditsStart, m)
				if fp[i].duration-s.End < 3 { // hasta el final: se marca hasta el final exacto
					s.End = fp[i].duration
				}
				if r.Credits == nil || s.End-s.Start > r.Credits.End-r.Credits.Start {
					r.Credits = &s
				}
			}
			// Con los dos vecinos inmediatos basta si ya hay ambas cosas.
			if d == 1 && r.Intro != nil && r.Credits != nil {
				break
			}
		}
		// An empty result means successfully read with no reliable match. Missing means unreadable.
		out[e.FileID] = r
	}
	return out, nil
}

func fingerprintWithin(ctx context.Context, f *os.File, start, dur float64) ([]uint32, error) {
	c, cancel := context.WithTimeout(ctx, fingerprintTimeout)
	defer cancel()
	return Fingerprint(c, f, start, dur)
}

// segment pasa una coincidencia a segundos del archivo. El paso k de la huella empieza en (k+1) pasos de audio.
func segment(offset float64, m Match) Segment {
	start := offset + float64(m.AStart+1)*FrameSeconds
	return Segment{Start: round1(start), End: round1(start + float64(m.Length)*FrameSeconds)}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
