package markers

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"

	"velora/internal/library"
	"velora/internal/store"
)

// Summary resume una tanda de detección.
type Summary struct {
	Seasons int `json:"temporadas"`
	Files   int `json:"archivos"`
	Found   int `json:"con_marcas"`
	Left    int `json:"pendientes"`
}

// RunPending analiza temporadas pendientes durante budget como mucho y guarda lo encontrado. Las huellas de cada archivo
// se guardan según se calculan: si la tanda se agota, alguien reproduce o el worker se reinicia, la siguiente sigue donde
// se quedó en vez de volver a leer la temporada (en un montaje remoto, minutos por episodio). Los archivos se abren
// siempre con os.Root dentro de las raíces de medios, en solo lectura.
func RunPending(ctx context.Context, st *store.Store, roots []string, budget time.Duration, report func(done, total int, item string)) (Summary, error) {
	var sum Summary
	started := time.Now()
	stop := func() bool { return time.Since(started) >= budget }
	total, _ := st.CountSeasonsNeedingMarkers(ctx, Version)
	finish := func(err error) (Summary, error) {
		sum.Left = max(0, total-sum.Seasons)
		return sum, err
	}
	for !stop() {
		seasons, err := st.SeasonsNeedingMarkers(ctx, Version, 5)
		if err != nil {
			return finish(err)
		}
		if len(seasons) == 0 {
			break
		}
		for _, sn := range seasons {
			if report != nil {
				report(sum.Seasons, max(total, sum.Seasons+1), sn.Title)
			}
			all, err := st.SeasonMarkerFiles(ctx, sn.ID, Version)
			if err != nil {
				return finish(err)
			}
			list := withNeighbours(all)
			eps := make([]Episode, 0, len(list))
			ids := make([]uuid.UUID, 0, len(list))
			for _, f := range list {
				f := f
				ids = append(ids, f.FileID)
				eps = append(eps, Episode{FileID: f.FileID, Duration: float64(f.DurationMS) / 1000, Skip: f.Analyzed,
					Open: func() (*os.File, error) {
						root, err := library.OpenRoot(f.LibPath, roots)
						if err != nil {
							return nil, err
						}
						defer root.Close()
						return root.Open(f.RelPath)
					},
					Load: func() ([]uint32, []uint32, bool) {
						in, cr, ok := st.Fingerprints(ctx, f, Version)
						return decode(in), decode(cr), ok
					},
					Store: func(in, cr []uint32) {
						if err := st.SaveFingerprints(ctx, f, Version, encode(in), encode(cr)); err != nil {
							slog.Warn("no se pudo guardar la huella de audio", "archivo", f.RelPath, "error", err)
						}
					}})
			}
			res, err := DetectSeasonUntil(ctx, eps, stop)
			if errors.Is(err, ErrStopped) {
				return finish(nil)
			}
			if err != nil {
				return finish(err)
			}
			unreadable := 0
			for _, f := range list {
				if f.Analyzed {
					continue
				}
				r, ok := res[f.FileID]
				if !ok {
					unreadable++
					continue
				}
				if err := st.SaveMarkers(ctx, f, Version, span(r.Intro), span(r.Credits)); err != nil {
					return finish(err)
				}
				sum.Files++
				if r.Intro != nil || r.Credits != nil {
					sum.Found++
				}
			}
			sum.Seasons++
			if unreadable > 0 { // sus vecinos conservan la huella: el reintento solo vuelve a leer lo que falló
				return finish(fmt.Errorf("no se pudieron analizar %d archivos de «%s»; quedan pendientes", unreadable, sn.Title))
			}
			if err := st.DropFingerprints(ctx, ids); err != nil {
				return finish(err)
			}
		}
	}
	return finish(nil)
}

// withNeighbours deja los archivos pendientes y los que están a dos episodios o menos de uno pendiente (con los que se
// comparan). Los ya analizados más lejanos no se vuelven a leer: un episodio nuevo no obliga a releer la temporada.
func withNeighbours(all []store.MarkerFile) []store.MarkerFile {
	out := make([]store.MarkerFile, 0, len(all))
	for i, f := range all {
		keep := false
		for j := max(0, i-2); j <= min(len(all)-1, i+2) && !keep; j++ {
			keep = !all[j].Analyzed
		}
		if keep {
			out = append(out, f)
		}
	}
	return out
}

func encode(v []uint32) []byte {
	out := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(out[4*i:], x)
	}
	return out
}

func decode(b []byte) []uint32 {
	out := make([]uint32, len(b)/4)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(b[4*i:])
	}
	return out
}

func span(s *Segment) *store.Span {
	if s == nil {
		return nil
	}
	return &store.Span{StartMS: int64(s.Start * 1000), EndMS: int64(s.End * 1000)}
}
