package core

import (
	"context"
	"testing"
)

// stubKato is a no-op KatoClient used to identify which client the registry returns.
type stubKato struct{ id string }

func (s stubKato) ListUseCases(context.Context) ([]UseCase, error)        { return nil, nil }
func (s stubKato) GetUseCase(context.Context, string) (Contract, error)   { return Contract{}, nil }
func (s stubKato) Run(context.Context, string, map[string]string) (RunResult, error) {
	return RunResult{}, nil
}

func TestRegistryListPreservesOrder(t *testing.T) {
	r := NewRegistry()
	r.Add(Cluster{Name: "prod", Label: "Production"}, stubKato{"p"})
	r.Add(Cluster{Name: "staging"}, stubKato{"s"})

	got := r.List()
	if len(got) != 2 || got[0].Name != "prod" || got[1].Name != "staging" {
		t.Fatalf("list order = %+v", got)
	}
	if got[0].Label != "Production" {
		t.Fatalf("label = %q", got[0].Label)
	}
}

func TestRegistryGet(t *testing.T) {
	r := NewRegistry()
	r.Add(Cluster{Name: "prod"}, stubKato{"p"})

	c, ok := r.Get("prod")
	if !ok {
		t.Fatal("prod should be present")
	}
	if c.(stubKato).id != "p" {
		t.Fatalf("got client %+v", c)
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("nope should be absent")
	}
}

func TestRegistryListReturnsCopy(t *testing.T) {
	r := NewRegistry()
	r.Add(Cluster{Name: "prod"}, stubKato{"p"})
	r.List()[0].Name = "mutated"
	if r.List()[0].Name != "prod" {
		t.Fatal("List() must return a copy, not the internal slice")
	}
}
