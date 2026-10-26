package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/orka-oss/orka_core/pathsafe"
)

// Chunked uploads are staged on disk under the storage root, never in
// memory: held in memory, any account could exhaust the process by starting
// uploads and never finishing them. Each upload is bounded, each user has a
// bounded number in flight, and abandoned ones are swept. On shared storage
// another instance sees the same parts, so uploads survive a restart or a
// load balancer switching instances.
const (
	uploadsDir         = ".orka_uploads"
	maxUploadBytes     = 512 << 20
	maxUploadsPerUser  = 8
	abandonedAfter     = time.Hour
	uploadSweepEvery   = 10 * time.Minute
	uploadMetaFileName = "meta.json"
)

type chunkManager struct {
	mu      sync.Mutex
	sweeper sync.Once
	now     func() time.Time
}

type uploadMeta struct {
	Filename string `json:"filename"`
	Total    int    `json:"total"`
}

func newChunkManager() *chunkManager { return &chunkManager{now: time.Now} }

func hashed(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:12])
}

func userUploadsDir(base, email string) string {
	return filepath.Join(base, uploadsDir, hashed(email))
}

func uploadDir(base, email, key string) string {
	return filepath.Join(userUploadsDir(base, email), hashed(key))
}

// accept stores one part. It reports whether every part is now present.
func (cm *chunkManager) accept(base, email, key, filename string, total, index int, data []byte) (bool, int, error) {
	if base == "" {
		return false, 0, errors.New("uploads need a storage root")
	}
	cm.startSweeper(base)
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if total <= 0 || index < 0 || index >= total {
		return false, 0, fmt.Errorf("invalid chunk index")
	}
	dir := uploadDir(base, email, key)
	meta, err := readUploadMeta(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if n := countDirs(userUploadsDir(base, email)); n >= maxUploadsPerUser {
			return false, 0, fmt.Errorf("too many unfinished uploads (%d); finish or wait for them to expire", n)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, 0, err
		}
		meta = uploadMeta{Filename: filename, Total: total}
		raw, _ := json.Marshal(meta)
		if err := os.WriteFile(filepath.Join(dir, uploadMetaFileName), raw, 0o600); err != nil {
			return false, 0, err
		}
	case err != nil:
		return false, 0, err
	}
	if meta.Filename != filename || meta.Total != total {
		return false, partCount(dir), fmt.Errorf("upload metadata changed")
	}
	if partBytes(dir, index)+int64(len(data)) > maxUploadBytes {
		return false, partCount(dir), fmt.Errorf("upload exceeds %d MB", maxUploadBytes>>20)
	}
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(index)+".part"), data, 0o600); err != nil {
		return false, 0, err
	}
	n := partCount(dir)
	return n == total, n, nil
}

// assemble streams the parts into the workspace file and removes the staging
// directory. The file appears whole or not at all (temp file, then rename).
func (cm *chunkManager) assemble(base, email, key string, root *os.Root, filename string) (int64, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	dir := uploadDir(base, email, key)
	meta, err := readUploadMeta(dir)
	if err != nil {
		return 0, err
	}
	if err := root.MkdirAll(filepath.Dir(filename), pathsafe.WorkspaceDirMode); err != nil {
		return 0, err
	}
	tmp := filename + ".uploading"
	out, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, pathsafe.WorkspaceFileMode)
	if err != nil {
		return 0, err
	}
	var size int64
	for i := 0; i < meta.Total; i++ {
		part, err := os.Open(filepath.Join(dir, strconv.Itoa(i)+".part"))
		if err == nil {
			var n int64
			n, err = io.Copy(out, part)
			part.Close()
			size += n
		}
		if err != nil {
			out.Close()
			_ = root.Remove(tmp)
			return 0, fmt.Errorf("assemble part %d: %w", i, err)
		}
	}
	if err := out.Close(); err != nil {
		_ = root.Remove(tmp)
		return 0, err
	}
	if err := root.Rename(tmp, filename); err != nil {
		_ = root.Remove(tmp)
		return 0, err
	}
	_ = os.RemoveAll(dir)
	return size, nil
}

func (cm *chunkManager) progress(base, email, key string) (int, int, bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	dir := uploadDir(base, email, key)
	meta, err := readUploadMeta(dir)
	if err != nil {
		return 0, 0, false
	}
	return partCount(dir), meta.Total, true
}

func (cm *chunkManager) startSweeper(base string) {
	cm.sweeper.Do(func() {
		go func() {
			for {
				cm.sweep(base)
				time.Sleep(uploadSweepEvery)
			}
		}()
	})
}

// sweep removes uploads nobody has added a part to for abandonedAfter.
func (cm *chunkManager) sweep(base string) int {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	removed := 0
	users, _ := os.ReadDir(filepath.Join(base, uploadsDir))
	for _, u := range users {
		ups, _ := os.ReadDir(filepath.Join(base, uploadsDir, u.Name()))
		for _, up := range ups {
			dir := filepath.Join(base, uploadsDir, u.Name(), up.Name())
			if cm.now().Sub(lastTouched(dir)) > abandonedAfter {
				if os.RemoveAll(dir) == nil {
					removed++
				}
			}
		}
	}
	return removed
}

func readUploadMeta(dir string) (uploadMeta, error) {
	var m uploadMeta
	raw, err := os.ReadFile(filepath.Join(dir, uploadMetaFileName))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(raw, &m)
}

func partCount(dir string) int {
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			n++
		}
	}
	return n
}

// partBytes is the staged size excluding the part being (re)written.
func partBytes(dir string, except int) int64 {
	entries, _ := os.ReadDir(dir)
	var total int64
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".part") || e.Name() == strconv.Itoa(except)+".part" {
			continue
		}
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
}

func lastTouched(dir string) time.Time {
	latest := time.Time{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	if latest.IsZero() {
		if info, err := os.Stat(dir); err == nil {
			latest = info.ModTime()
		}
	}
	return latest
}

func countDirs(dir string) int {
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}
