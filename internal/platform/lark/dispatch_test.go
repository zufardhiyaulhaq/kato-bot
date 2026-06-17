package lark

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zufardhiyaulhaq/kato-bot/internal/core"
)

// blockingKato holds Run until release is closed, so a test can pin the semaphore slot.
type blockingKato struct {
	contract core.Contract
	started  chan struct{} // closed-ish: receives once Run is entered
	release  chan struct{} // Run returns after this is closed
}

func (b *blockingKato) ListUseCases(context.Context) ([]core.UseCase, error) { return nil, nil }
func (b *blockingKato) GetUseCase(context.Context, string) (core.Contract, error) {
	return b.contract, nil
}
func (b *blockingKato) Run(context.Context, string, map[string]string) (core.RunResult, error) {
	b.started <- struct{}{}
	<-b.release
	return core.RunResult{Phase: "Completed", Summary: "ok"}, nil
}

// recordingRenderer records which render method was last called (thread-safe).
type recordingRenderer struct {
	mu    sync.Mutex
	kinds   []string
	lastErr string // last message passed to RenderError
}

func (r *recordingRenderer) note(k string) {
	r.mu.Lock()
	r.kinds = append(r.kinds, k)
	r.mu.Unlock()
}
func (r *recordingRenderer) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.kinds) == 0 {
		return ""
	}
	return r.kinds[len(r.kinds)-1]
}
func (r *recordingRenderer) RenderPicker(context.Context, core.Reply, []core.UseCase) error {
	r.note("picker")
	return nil
}
func (r *recordingRenderer) RenderForm(context.Context, core.Reply, core.Contract, map[string]string, string) error {
	r.note("form")
	return nil
}
func (r *recordingRenderer) RenderRunning(context.Context, core.Reply, string, map[string]string) error {
	r.note("running")
	return nil
}
func (r *recordingRenderer) RenderResult(context.Context, core.Reply, string, map[string]string, core.RunResult) error {
	r.note("result")
	return nil
}

func (r *recordingRenderer) RenderError(_ context.Context, _ core.Reply, msg string) error {
	r.mu.Lock()
	r.kinds = append(r.kinds, "error")
	r.lastErr = msg
	r.mu.Unlock()
	return nil
}

// errMsg returns the last message passed to RenderError.
func (r *recordingRenderer) errMsg() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

// TestDispatchSemaphoreCapsRuns verifies that with MaxConcurrent=1, a second submit
// while the first run is still in flight is rejected with a "busy" error card and does
// not start a second run.
func TestDispatchSemaphoreCapsRuns(t *testing.T) {
	bk := &blockingKato{
		contract: core.Contract{Name: "uc"},
		started:  make(chan struct{}, 1),
		release:  make(chan struct{}),
	}
	r := &recordingRenderer{}
	a := &Adapter{
		Core:          &core.Core{Kato: bk, R: r},
		RunTimeout:    5 * time.Second,
		MaxConcurrent: 1,
	}

	submit := core.SubmitForm{Reply: core.Reply{MessageID: "card1"}, Name: "uc", Inputs: map[string]string{}}

	// First submit: acquires the only slot and blocks inside Run.
	a.dispatch(context.Background(), submit)
	select {
	case <-bk.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first run never started")
	}

	// Second submit while the slot is held: must be rejected as busy, no new run.
	// (bk.Run sends to bk.started on entry; a second entry would block, so the fact
	// that we never see a second "started" confirms no second run was admitted.)
	a.dispatch(context.Background(), submit)
	if got := r.last(); got != "error" {
		t.Fatalf("over-cap submit: last render = %q, want error(busy)", got)
	}
	if msg := r.errMsg(); !strings.Contains(msg, "busy") {
		t.Fatalf("expected a busy message, got %q", msg)
	}
	select {
	case <-bk.started:
		t.Fatal("a second run was started despite the cap")
	default:
	}

	// Release the first run so its goroutine completes and frees the slot cleanly.
	close(bk.release)
}
