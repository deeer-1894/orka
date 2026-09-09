package pathsafe

import (
	"path/filepath"
	"strings"
	"testing"
)

// The point of the split: two conversations of the same account must not be
// able to see each other's files. Everything else here is in service of that.
func TestConversationsGetSeparateWorkspaces(t *testing.T) {
	a := Workspace("/data", "u@x.com", "conv-a")
	b := Workspace("/data", "u@x.com", "conv-b")
	if a == b {
		t.Fatalf("two conversations resolved to the same directory: %s", a)
	}
	// Neither may contain the other, or a listing of one would walk into it.
	if rel, err := filepath.Rel(a, b); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("%s is inside %s", b, a)
	}
	if want := filepath.Join(UserRoot("/data", "u@x.com"), "conv-a"); a != want {
		t.Fatalf("workspace = %s, want %s", a, want)
	}
}

func TestWorkspacesOfDifferentUsersNeverMeet(t *testing.T) {
	if a, b := Workspace("/data", "a@x.com", "c1"), Workspace("/data", "b@x.com", "c1"); a == b {
		t.Fatalf("two accounts share a workspace for the same conversation id: %s", a)
	}
}

// No conversation = the account root. Scheduled tasks and the quant pipeline
// have no conversation, and giving each invocation its own directory would
// scatter output the next run expects to find.
func TestNoConversationFallsBackToTheAccountRoot(t *testing.T) {
	root := UserRoot("/data", "u@x.com")
	for _, conv := range []string{"", "   "} {
		if got := Workspace("/data", "u@x.com", conv); got != root {
			t.Errorf("conv %q gave %s, want the account root %s", conv, got, root)
		}
	}
}

// A conversation id reaches the filesystem, so it must not be able to span
// directories — including up and out of the account it belongs to.
func TestAHostileConversationIDStaysOneSegment(t *testing.T) {
	root := UserRoot("/data", "u@x.com")
	for _, conv := range []string{"../../etc", "a/b", `a\b`, ".."} {
		got := Workspace("/data", "u@x.com", conv)
		rel, err := filepath.Rel(root, got)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("conv %q escaped the account root: %s", conv, got)
			continue
		}
		if strings.Contains(rel, string(filepath.Separator)) {
			t.Errorf("conv %q spanned directories: %s", conv, rel)
		}
	}
}

// Containment is still enforced by Resolve on top of the workspace, so a
// traversing FILE path cannot climb into a sibling conversation.
func TestResolveCannotClimbIntoASiblingConversation(t *testing.T) {
	ws := Workspace("/data", "u@x.com", "conv-a")
	for _, rel := range []string{"../conv-b/secret.md", "../../other@x.com/conv-b/secret.md", "../../../etc/passwd"} {
		if got, err := Resolve(ws, rel); err == nil {
			t.Errorf("%q resolved to %s instead of being rejected", rel, got)
		}
	}
}

// A user with no email still gets a stable, contained root rather than the
// base directory itself.
func TestAnonymousStillGetsItsOwnRoot(t *testing.T) {
	got := Workspace("/data", "", "c1")
	if !strings.Contains(got, "_anonymous") {
		t.Fatalf("anonymous workspace = %s", got)
	}
}
