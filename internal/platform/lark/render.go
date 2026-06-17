package lark

import (
	"context"

	"github.com/zufardhiyaulhaq/kato-bot/internal/core"
)

// sender is the minimal Lark message surface the renderer needs. The real impl
// (sender.go) wraps larkim; tests use a fake.
type sender interface {
	// Reply posts a new card as a reply to toMessageID (the user's message).
	Reply(ctx context.Context, toMessageID, cardJSON string) error
	// Patch replaces the card content of an existing bot message.
	Patch(ctx context.Context, messageID, cardJSON string) error
}

// Renderer implements core.Renderer for Lark by building cards and sending/patching them.
type Renderer struct{ S sender }

// emit sends a card: patch the existing bot card when r.MessageID is set, else reply
// to the user's message (the first card in a flow).
func (rd *Renderer) emit(ctx context.Context, r core.Reply, cardJSON string) error {
	if r.MessageID != "" {
		return rd.S.Patch(ctx, r.MessageID, cardJSON)
	}
	return rd.S.Reply(ctx, r.InReplyTo, cardJSON)
}

func (rd *Renderer) RenderPicker(ctx context.Context, r core.Reply, ucs []core.UseCase) error {
	return rd.emit(ctx, r, buildPickerCard(ucs))
}

func (rd *Renderer) RenderForm(ctx context.Context, r core.Reply, c core.Contract, prefill map[string]string, formErr string) error {
	return rd.emit(ctx, r, buildFormCard(c, prefill, formErr))
}

func (rd *Renderer) RenderRunning(ctx context.Context, r core.Reply, useCase string, inputs map[string]string) error {
	return rd.emit(ctx, r, buildRunningCard(useCase, inputs))
}

func (rd *Renderer) RenderResult(ctx context.Context, r core.Reply, useCase string, inputs map[string]string, res core.RunResult) error {
	return rd.emit(ctx, r, buildResultCard(useCase, res))
}

func (rd *Renderer) RenderError(ctx context.Context, r core.Reply, msg string) error {
	return rd.emit(ctx, r, buildErrorCard(msg))
}
