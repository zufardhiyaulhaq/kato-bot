// Package lark is the Lark (Feishu) chat adapter for kato-bot.
package lark

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zufardhiyaulhaq/kato-bot/internal/core"
)

// jsonStr marshals a card object to a compact JSON string. Card objects are built
// from map[string]any so the structure stays declarative and easy to assert in tests.
func jsonStr(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// Card objects here are always marshalable; a failure is a programming error.
		return fmt.Sprintf(`{"elements":[{"tag":"markdown","content":"card build error: %s"}]}`, err)
	}
	return string(b)
}

func header(title string) map[string]any {
	return map[string]any{"title": map[string]any{"tag": "plain_text", "content": title}}
}

func markdown(content string) map[string]any {
	return map[string]any{"tag": "markdown", "content": content}
}

// buildPickerCard lists each UseCase with a Select button (ready ones) or a disabled note.
func buildPickerCard(ucs []core.UseCase) string {
	elements := []any{markdown("🔧 **kato** — pick a troubleshooting flow")}
	for _, uc := range ucs {
		elements = append(elements, map[string]any{"tag": "hr"})
		elements = append(elements, markdown(fmt.Sprintf("**%s**\n%s", uc.Name, uc.Description)))
		if uc.Ready {
			elements = append(elements, map[string]any{
				"tag": "action",
				"actions": []any{map[string]any{
					"tag":  "button",
					"text": map[string]any{"tag": "plain_text", "content": "Select ▸"},
					"type": "primary",
					"value": map[string]any{"action": "pick", "usecase": uc.Name},
				}},
			})
		} else {
			elements = append(elements, markdown("_not ready (failed validation in cluster)_"))
		}
	}
	return jsonStr(map[string]any{
		"config":   map[string]any{"wide_screen_mode": true},
		"header":   header("kato"),
		"elements": elements,
	})
}

// buildFormCard renders one input per declared input, prefilled, with an optional error
// banner and a Run submit button.
//
// This is a Card JSON 2.0 card ("schema":"2.0" + "body"): the `form` container and
// `input` components ONLY work in 2.0 — in the legacy format the inputs render but the
// submit button fires no callback. The submit button uses `form_action_type:"submit"`
// plus a `behaviors` callback (the legacy `action_type:"form_submit"` + `value` does
// nothing). The behavior's value lands in the callback's action.value and the input
// values in action.form_value, which decode.go reads.
func buildFormCard(c core.Contract, prefill map[string]string, formErr string) string {
	formElems := []any{}
	if formErr != "" {
		formElems = append(formElems, markdown("⚠️ "+formErr))
	}
	formElems = append(formElems, markdown(fmt.Sprintf("🔧 **%s**\n%s", c.Name, c.Description)))
	for _, in := range c.Inputs {
		label := in.Name
		if in.Required {
			label += " *"
		}
		// Label as its own markdown line; the input carries only verified 2.0 attributes.
		formElems = append(formElems, markdown("**"+label+"**"))
		input := map[string]any{
			"tag":         "input",
			"name":        in.Name,
			"placeholder": map[string]any{"tag": "plain_text", "content": in.Name},
			"required":    in.Required,
		}
		if v := prefill[in.Name]; v != "" {
			input["default_value"] = v
		}
		formElems = append(formElems, input)
	}
	formElems = append(formElems, map[string]any{
		"tag":              "button",
		"text":             map[string]any{"tag": "plain_text", "content": "▶ Run troubleshoot"},
		"type":             "primary",
		"form_action_type": "submit",
		"name":             "submit",
		"behaviors": []any{
			map[string]any{"type": "callback", "value": map[string]any{"action": "run", "usecase": c.Name}},
		},
	})
	form := map[string]any{"tag": "form", "name": "kato_form", "elements": formElems}
	return jsonStr(map[string]any{
		"schema": "2.0",
		"header": map[string]any{"title": map[string]any{"tag": "plain_text", "content": c.Name}},
		"body":   map[string]any{"elements": []any{form}},
	})
}

// kvLine renders the inputs as "k=v  k=v" display text. Order is intentionally
// arbitrary (map iteration) — this is display-only and has no order-sensitive consumer.
func kvLine(inputs map[string]string) string {
	parts := make([]string, 0, len(inputs))
	for k, v := range inputs {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, "  ")
}

// buildRunningCard is the immediate ack repaint shown while kato runs.
func buildRunningCard(useCase string, inputs map[string]string) string {
	return jsonStr(map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": header("kato"),
		"elements": []any{
			markdown(fmt.Sprintf("⏳ **Running %s…**", useCase)),
			markdown(kvLine(inputs)),
			markdown("_This can take up to ~30s while kato runs the checks and summarizes._"),
		},
	})
}

// buildResultCard renders the final summary (or a friendly error) plus a Run-again button.
func buildResultCard(useCase string, res core.RunResult) string {
	var elements []any
	if res.Err != nil {
		elements = []any{
			markdown(fmt.Sprintf("❌ **%s — could not run**", useCase)),
			markdown(res.Err.Error()),
		}
	} else {
		// Phase icon: kato reports "Completed" on success; anything else (e.g. "Failed") is not green.
		icon := "✅"
		if res.Phase != "Completed" {
			icon = "❌"
		}
		head := icon + " **" + useCase + " — " + res.Phase + "**"
		elements = []any{markdown(head)}
		if res.Warning != "" {
			elements = append(elements, markdown("⚠️ "+res.Warning))
		}
		elements = append(elements,
			map[string]any{"tag": "hr"},
			markdown("📋 **Summary**\n"+res.Summary),
			map[string]any{"tag": "hr"},
			markdown("_run: "+res.Run+"_"),
		)
	}
	elements = append(elements, map[string]any{
		"tag": "action",
		"actions": []any{map[string]any{
			"tag":   "button",
			"text":  map[string]any{"tag": "plain_text", "content": "↻ Run again"},
			"value": map[string]any{"action": "pick", "usecase": useCase},
		}},
	})
	return jsonStr(map[string]any{
		"config":   map[string]any{"wide_screen_mode": true},
		"header":   header(useCase),
		"elements": elements,
	})
}

// buildErrorCard is a standalone error (e.g. kato unreachable at list time).
func buildErrorCard(msg string) string {
	return jsonStr(map[string]any{
		"config":   map[string]any{"wide_screen_mode": true},
		"header":   header("kato"),
		"elements": []any{markdown("❌ " + msg)},
	})
}
