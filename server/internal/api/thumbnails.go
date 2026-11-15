package api

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"velora/internal/imaging"
	"velora/internal/library"
	"velora/internal/store"
)

// Authorization is always checked by itemImage before accessing this derived cache.
// A small fixed set of sizes and a 256-file limit bound disk and CPU consumption.
var thumbnailMu [16]sync.Mutex
var thumbnailSlots = make(chan struct{}, 2)
var thumbnailEviction sync.Mutex

func (s *Server) serveThumbnail(w http.ResponseWriter, r *http.Request, img store.Image, kind string) bool {
	requested, _ := strconv.Atoi(r.URL.Query().Get("w"))
	if requested <= 0 {
		return false
	}
	width := 1280
	for _, size := range []int{160, 320, 640, 1280} {
		if requested <= size {
			width = size
			break
		}
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%s:%d:%d", img.Source, img.LibPath, img.Path, kind, img.UpdatedAt.UnixNano(), width))))
	dir := filepath.Join(s.cfg.ConfigDir, "image-cache")
	ext := ".jpg"
	if kind == "logo" {
		ext = ".png"
	}
	path := filepath.Join(dir, key+ext)
	serve := func() bool {
		f, err := os.Open(path)
		if err != nil {
			return false
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return false
		}
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "private, max-age=86400")
		} else {
			w.Header().Set("Cache-Control", "private, no-cache")
		}
		w.Header().Set("ETag", `"`+key+`"`)
		http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
		return true
	}
	if serve() {
		return true
	}
	// Coalesce misses; small thumbnails must not launch a decoder for every card at once.
	lock := &thumbnailMu[int(key[0])%len(thumbnailMu)]
	lock.Lock()
	defer lock.Unlock()
	if serve() {
		return true
	}
	select {
	case thumbnailSlots <- struct{}{}:
		defer func() { <-thumbnailSlots }()
	case <-r.Context().Done():
		return true
	}
	var root *os.Root
	var err error
	if img.Source == "local" {
		root, err = library.OpenRoot(img.LibPath, s.cfg.MediaRoots)
	} else {
		root, err = os.OpenRoot(filepath.Join(s.cfg.ConfigDir, "metadata"))
	}
	if err != nil {
		return false
	}
	defer root.Close()
	f, err := root.Open(img.Path)
	if err != nil {
		return false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, imaging.MaxUploadBytes+1))
	if err != nil {
		return false
	}
	thumb, err := imaging.Thumbnail(raw, kind, width)
	if err != nil {
		return false
	}
	if err = os.MkdirAll(dir, 0750); err != nil {
		return false
	}
	thumbnailEviction.Lock()
	defer thumbnailEviction.Unlock()
	trimThumbnailCache(dir)
	// File names derive only from trusted image records, never from query version strings.
	tmp, err := os.CreateTemp(dir, "thumb-*")
	if err != nil {
		return false
	}
	name := tmp.Name()
	defer os.Remove(name)
	_, err = tmp.Write(thumb.Data)
	closeErr := tmp.Close()
	if err != nil || closeErr != nil {
		return false
	}
	if err = os.Rename(name, path); err != nil {
		return false
	}
	return serve()
}

func trimThumbnailCache(dir string) {
	entries, _ := os.ReadDir(dir)
	if len(entries) >= 256 {
		sort.Slice(entries, func(i, j int) bool {
			a, e1 := entries[i].Info()
			b, e2 := entries[j].Info()
			return e1 == nil && e2 == nil && a.ModTime().Before(b.ModTime())
		})
		for _, e := range entries[:len(entries)-255] {
			if !e.IsDir() {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
}
