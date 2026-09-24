// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package model_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/ax/internal/model"
)

func TestRegistry_RegisterLookupKnown(t *testing.T) {
	r := model.NewRegistry()
	if err := r.Register("OpenAI", func(model.Config, *http.Client) model.Provider { return nil }); err != nil {
		t.Fatalf("register: %v", err)
	}
	for _, name := range []string{"openai", "OPENAI", " openai "} {
		if _, ok := r.Lookup(name); !ok {
			t.Errorf("expected lookup %q to succeed", name)
		}
	}
	if _, ok := r.Lookup("anthropic"); ok {
		t.Error("expected unregistered lookup to fail")
	}
	if err := r.Register("Anthropic ", func(model.Config, *http.Client) model.Provider { return nil }); err != nil {
		t.Fatalf("register: %v", err)
	}
	got := r.Known()
	if len(got) != 2 || got[0] != "anthropic" || got[1] != "openai" {
		t.Errorf("expected sorted known names [anthropic openai], got %v", got)
	}
}

func TestRegistry_RejectsInvalidRegistration(t *testing.T) {
	r := model.NewRegistry()
	factory := func(model.Config, *http.Client) model.Provider { return nil }
	if err := r.Register("dup", factory); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register("dup", factory); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Errorf("expected duplicate rejection, got %v", err)
	}
	if err := r.Register("", factory); err == nil {
		t.Error("expected empty name rejection")
	}
	if err := r.Register("nil-factory", nil); err == nil {
		t.Error("expected nil factory rejection")
	}
}

func TestValidateProvider(t *testing.T) {
	for _, ok := range []string{"", "google", "Google", "openai", "anthropic"} {
		if err := model.ValidateProvider(ok); err != nil {
			t.Errorf("expected %q to validate, got %v", ok, err)
		}
	}
	err := model.ValidateProvider("gemini-flash")
	if err == nil {
		t.Fatal("expected unknown provider to be rejected")
	}
	for _, want := range []string{"gemini-flash", "google", "openai", "anthropic"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention %q, got %v", want, err)
		}
	}
}

func TestKnown_IncludesBuiltins(t *testing.T) {
	got := model.Known()
	for _, want := range []string{"anthropic", "google", "openai"} {
		found := false
		for _, name := range got {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected built-in provider %q in Known() %v", want, got)
		}
	}
}

// Guard against accidental fabrication: a nil factory must never be reachable
// through Lookup for the built-ins.
func TestLookup_BuiltinsAreFactories(t *testing.T) {
	for _, name := range []string{"google", "openai", "anthropic"} {
		factory, ok := model.Lookup(name)
		if !ok || factory == nil {
			t.Fatalf("expected a factory for %q", name)
		}
		p := factory(model.Config{APIKey: "k", Model: "m"}, http.DefaultClient)
		if p == nil || p.Name() != name {
			t.Errorf("factory for %q built %v", name, p)
		}
	}
}
