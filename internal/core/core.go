package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Core ties the inbound intents to kato + the renderer. It is fully synchronous:
// the only slow work (a validated run) is returned as a `deferred` thunk for the
// adapter to run in a goroutine, preserving the platform's fast-ack requirement.
type Core struct {
	Kato KatoClient
	R    Renderer
}

// Handle processes one intent. It returns a non-nil deferred ONLY for a validated
// SubmitForm; the caller must run it (typically `go deferred(bgCtx)`) after acking.
// A returned error is an internal/renderer failure the adapter should log.
func (c *Core) Handle(ctx context.Context, in Intent) (deferred func(context.Context) error, err error) {
	switch v := in.(type) {
	case ListUseCases:
		ucs, e := c.Kato.ListUseCases(ctx)
		if e != nil {
			return nil, c.R.RenderError(ctx, v.Reply, friendlyKatoError(e))
		}
		return nil, c.R.RenderPicker(ctx, v.Reply, ucs)

	case PickUseCase:
		ct, e := c.Kato.GetUseCase(ctx, v.Name)
		if e != nil {
			return nil, c.R.RenderError(ctx, v.Reply, friendlyKatoError(e))
		}
		return nil, c.R.RenderForm(ctx, v.Reply, ct, nil, "")

	case SubmitForm:
		ct, e := c.Kato.GetUseCase(ctx, v.Name)
		if e != nil {
			return nil, c.R.RenderError(ctx, v.Reply, friendlyKatoError(e))
		}
		if missing := missingRequired(ct, v.Inputs); len(missing) > 0 {
			return nil, c.R.RenderForm(ctx, v.Reply, ct, v.Inputs,
				"required: "+strings.Join(missing, ", "))
		}
		if e := c.R.RenderRunning(ctx, v.Reply, v.Name, v.Inputs); e != nil {
			return nil, e
		}
		reply, name, inputs, contract := v.Reply, v.Name, v.Inputs, ct
		return func(dctx context.Context) error {
			res, runErr := c.Kato.Run(dctx, name, inputs)
			if runErr != nil {
				// A 400 means kato rejected the inputs — send the user back to the
				// form with the reason so they can fix and resubmit (spec: 400 → form
				// error). Any other failure becomes a friendly result-card error.
				var se HTTPStatusError
				if errors.As(runErr, &se) && se.HTTPStatus() == 400 {
					return c.R.RenderForm(dctx, reply, contract, inputs, friendlyKatoError(runErr))
				}
				res = RunResult{Err: &RunError{Msg: friendlyKatoError(runErr)}}
			}
			return c.R.RenderResult(dctx, reply, name, inputs, res)
		}, nil

	default:
		return nil, fmt.Errorf("unknown intent %T", in)
	}
}

// friendlyKatoError turns a kato client error into a concise, user-facing message.
// Status-bearing errors (kato.APIError, via the HTTPStatusError interface) map to
// status-specific text; anything else (transport/timeout) is reported as unreachable.
func friendlyKatoError(err error) string {
	var se HTTPStatusError
	if errors.As(err, &se) {
		switch s := se.HTTPStatus(); {
		case s == 400:
			return "invalid inputs: " + se.Detail()
		case s == 404:
			return "use case not found in the cluster"
		case s == 422:
			return "this use case failed validation in the cluster"
		case s == 429:
			return "kato is busy (too many concurrent runs) — try again shortly"
		case s >= 500:
			return "kato had an internal error — try again shortly"
		default:
			return "kato returned an error: " + se.Detail()
		}
	}
	return "couldn't reach kato: " + err.Error()
}

// missingRequired returns the sorted names of required inputs that are absent or blank.
func missingRequired(ct Contract, inputs map[string]string) []string {
	var missing []string
	for _, in := range ct.Inputs {
		if in.Required && strings.TrimSpace(inputs[in.Name]) == "" {
			missing = append(missing, in.Name)
		}
	}
	sort.Strings(missing)
	return missing
}
