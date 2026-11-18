package store

import (
	"context"
	"errors"
	"path"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ItemSource contains only paths relative to the library. It is read from the
// catalogue, so identifying a title never opens or probes the media itself.
type ItemSource struct {
	Library  string `json:"library"`
	Folder   string `json:"folder"`
	Filename string `json:"filename,omitempty"`
	Missing  bool   `json:"missing"`
}

func (s *Store) AdminItemSources(ctx context.Context, id uuid.UUID) ([]ItemSource, error) {
	var kind, folder, library string
	err := s.Pool.QueryRow(ctx, `select i.kind, i.content_path, lb.name
  from media_items i join libraries lb on lb.id=i.library_id where i.id=$1`, id).Scan(&kind, &folder, &library)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// A series is identified by its own folder, never an episode or season name.
	if kind == "series" || kind == "season" {
		return []ItemSource{{Library: library, Folder: folder}}, nil
	}
	rows, err := s.Pool.Query(ctx, `select lb.name, f.rel_path, f.missing_since is not null
  from media_files f join library_paths lp on lp.id=f.library_path_id
  join libraries lb on lb.id=lp.library_id where f.item_id=$1
  order by (f.missing_since is not null), lb.name, f.rel_path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ItemSource{}
	for rows.Next() {
		var entry ItemSource
		var rel string
		if err := rows.Scan(&entry.Library, &rel, &entry.Missing); err != nil {
			return nil, err
		}
		entry.Folder, entry.Filename = path.Dir(rel), path.Base(rel)
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Pending films can have a folder before a main media file is available.
	if len(out) == 0 {
		out = append(out, ItemSource{Library: library, Folder: folder})
	}
	return out, nil
}
