package lark

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zufardhiyaulhaq/kato-bot/internal/core"
)

func asMap(t *testing.T, jsonStr string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		t.Fatalf("card is not valid JSON: %v\n%s", err, jsonStr)
	}
	return m
}

func TestBuildPickerCard(t *testing.T) {
	card := buildPickerCard([]core.UseCase{
		{Name: "pod-crashloop", Description: "Diagnose crashloop", Ready: true},
		{Name: "broken", Description: "x", Ready: false},
	})
	m := asMap(t, card)
	if _, ok := m["elements"]; !ok {
		t.Fatal("no elements")
	}
	// The card must mention each usecase name and carry a pick action value for the ready one.
	if !strings.Contains(card, "pod-crashloop") {
		t.Error("missing usecase name")
	}
	if !strings.Contains(card, `"action":"pick"`) || !strings.Contains(card, `"usecase":"pod-crashloop"`) {
		t.Error("missing pick action value")
	}
}

func TestBuildFormCard(t *testing.T) {
	c := core.Contract{Name: "pod-crashloop", Description: "d", Inputs: []core.InputDecl{
		{Name: "namespace", Required: true}, {Name: "pod", Required: true},
	}}
	card := buildFormCard(c, map[string]string{"namespace": "payments"}, "required: pod")
	if !strings.Contains(card, "namespace") || !strings.Contains(card, "pod") {
		t.Error("missing input names")
	}
	if !strings.Contains(card, "payments") {
		t.Error("missing prefill value")
	}
	if !strings.Contains(card, "required: pod") {
		t.Error("missing form error text")
	}
	if !strings.Contains(card, `"action":"run"`) || !strings.Contains(card, `"usecase":"pod-crashloop"`) {
		t.Error("missing run action value")
	}
	asMap(t, card) // valid JSON
}

func TestBuildRunningCard(t *testing.T) {
	card := buildRunningCard("pod-crashloop", map[string]string{"namespace": "payments"})
	if !strings.Contains(card, "Running") || !strings.Contains(card, "pod-crashloop") {
		t.Error("running card content")
	}
	if !strings.Contains(card, "namespace=payments") {
		t.Error("missing inputs line")
	}
	asMap(t, card)
}

func TestBuildResultCardCompleted(t *testing.T) {
	card := buildResultCard("pod-crashloop", core.RunResult{
		Run: "pod-crashloop-abc", Phase: "Completed", Summary: "It is OOMKilled.",
	})
	if !strings.Contains(card, "It is OOMKilled.") || !strings.Contains(card, "pod-crashloop-abc") {
		t.Error("result card content")
	}
	if !strings.Contains(card, "✅") {
		t.Error("completed phase should show a green check")
	}
	if !strings.Contains(card, `"action":"pick"`) {
		t.Error("missing Run again action")
	}
	asMap(t, card)
}

func TestBuildResultCardFailedPhase(t *testing.T) {
	card := buildResultCard("pod-crashloop", core.RunResult{
		Run: "pod-crashloop-abc", Phase: "Failed", Summary: "step errored",
	})
	if strings.Contains(card, "✅") {
		t.Error("failed phase must not show a green check")
	}
	if !strings.Contains(card, "❌") || !strings.Contains(card, "Failed") {
		t.Error("failed phase should show a red cross and the phase")
	}
	asMap(t, card)
}

func TestBuildResultCardError(t *testing.T) {
	card := buildResultCard("uc", core.RunResult{Err: &core.RunError{Msg: "kato is busy"}})
	if !strings.Contains(card, "kato is busy") {
		t.Error("error not shown")
	}
	asMap(t, card)
}
