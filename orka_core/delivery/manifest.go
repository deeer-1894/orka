package delivery

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
)

const maxManifestBytes int64 = 2 << 20

func commitManifest(store *os.Root, run string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	temp := path.Join(run, "manifest.pending")
	file, err := store.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := store.Rename(temp, path.Join(run, "manifest.json")); err != nil {
		return err
	}
	dir, err := store.Open(run)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func loadManifest(store *os.Root, conv, run string) (Manifest, error) {
	data, err := readBounded(store, path.Join(run, "manifest.json"), maxManifestBytes)
	if os.IsNotExist(err) {
		return Manifest{}, ErrNotFound
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: cannot read manifest", ErrIntegrity)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, ErrIntegrity
	}
	if m.Version != manifestVersion || m.RunID != run || m.ConversationID != conv || m.CreatedAt.IsZero() || len(m.Files) == 0 || len(m.Files) > MaxFiles {
		return Manifest{}, ErrIntegrity
	}
	seen := map[string]bool{}
	total := int64(0)
	for _, f := range m.Files {
		hash, err := hex.DecodeString(f.SHA256)
		if !validPath(f.Path) || seen[f.Path] || f.Size < 0 || f.Size > MaxFileBytes || err != nil || len(hash) != 32 {
			return Manifest{}, ErrIntegrity
		}
		seen[f.Path] = true
		total += f.Size
		if total > MaxTotalBytes {
			return Manifest{}, ErrIntegrity
		}
	}
	return m, nil
}
func readBounded(root *os.Root, rel string, limit int64) ([]byte, error) {
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrIntegrity
	}
	file, err := openSource(root, rel)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		return nil, ErrIntegrity
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrIntegrity
	}
	return data, nil
}
