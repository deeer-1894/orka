package service

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/schema"
	"path/filepath"
)

func resumeJournal(f *journalFile) *runResume {
	if f == nil {
		return nil
	}
	return &runResume{Messages: resumeMessages(f), Checkpoint: f.Checkpoint, Delegates: append([]delegateRecord(nil), f.Delegates...), Steps: len(f.Messages)}
}
func (j *runJournal) inherit(r *runResume) {
	if j == nil || r == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seed = append([]*schema.Message(nil), r.Messages...)
	j.delegates = append([]delegateRecord(nil), r.Delegates...)
	j.dirty = true
}

// Preview size must not determine what survives recovery. Keep the complete
// archive in the successor journal and expose a user-workspace reference too.
func exposeRecoveryArchive(ctx context.Context, backend filesystem.Backend, dir string) {
	r := runResumeFrom(ctx)
	if r == nil || len(r.Delegates) == 0 || backend == nil {
		return
	}
	b, err := json.Marshal(r.Delegates)
	if err != nil {
		return
	}
	p := filepath.Join(dir, "recovery-delegates.json")
	if backend.Write(ctx, &filesystem.WriteRequest{FilePath: p, Content: string(b)}) != nil {
		return
	}
	r.Messages = append(r.Messages, runtimeUserMessage("Complete prior delegate observations are retained at "+p+". Read relevant sections only; verify uncertain operations before retrying."))
}
