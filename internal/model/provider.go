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

package model

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Provider renders generations against one model API wire format. Built-in
// implementations register themselves from init() functions; a new provider is
// an adapter, not a schema change.
type Provider interface {
	// Name is the lowercase registry key ("google", "openai", "anthropic").
	Name() string
	// Generate issues a single generation. Every failure is a *ProviderError:
	// providers never fabricate completions.
	Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error)
}

// ProviderFactory builds a Provider bound to a client configuration and HTTP
// client.
type ProviderFactory func(cfg Config, httpClient *http.Client) Provider

// Registry maps lowercase provider names to factories. The zero value is not
// ready for use; call NewRegistry.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]ProviderFactory
}

// NewRegistry returns an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{factories: map[string]ProviderFactory{}}
}

// Register installs a factory under the lowercased, trimmed name. It rejects
// empty names, nil factories, and duplicates so configuration mistakes surface
// at startup instead of at generation time.
func (r *Registry) Register(name string, factory ProviderFactory) error {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return fmt.Errorf("provider name must not be empty")
	}
	if factory == nil {
		return fmt.Errorf("provider %q: factory must not be nil", key)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.factories[key]; dup {
		return fmt.Errorf("provider %q is already registered", key)
	}
	r.factories[key] = factory
	return nil
}

// Lookup returns the factory registered under the lowercased name.
func (r *Registry) Lookup(name string) (ProviderFactory, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.factories[strings.ToLower(strings.TrimSpace(name))]
	return f, ok
}

// Known returns the sorted list of registered provider names.
func (r *Registry) Known() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// defaultRegistry holds the built-in providers.
var defaultRegistry = NewRegistry()

// Register installs a built-in provider factory in the default registry. It
// panics on error and is meant to be called from provider init() functions.
func Register(name string, factory ProviderFactory) {
	if err := defaultRegistry.Register(name, factory); err != nil {
		panic(fmt.Sprintf("model: registering provider: %v", err))
	}
}

// Lookup returns the built-in factory registered under name.
func Lookup(name string) (ProviderFactory, bool) {
	return defaultRegistry.Lookup(name)
}

// Known returns the sorted names of the built-in providers.
func Known() []string {
	return defaultRegistry.Known()
}

// ValidateProvider reports whether name names a registered provider. The empty
// string is valid and means the default provider ("google"); matching is
// case-insensitive. Errors name the valid values so `ax apply` can reject a
// typo before any task consumes the Model.
func ValidateProvider(name string) error {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		key = ProviderGoogle
	}
	if _, ok := defaultRegistry.Lookup(key); ok {
		return nil
	}
	return fmt.Errorf("unknown provider %q (valid providers: %s)", name, strings.Join(Known(), ", "))
}

// maxErrorBody bounds how much of a provider response body is carried in an
// error message.
const maxErrorBody = 256

// ProviderError is the only failure shape returned by Provider.Generate: an
// empty key, an HTTP 4xx/5xx (carrying the provider status and body), a
// transport failure, or a malformed response. It never carries fallback text —
// providers do not fabricate completions.
type ProviderError struct {
	// Provider is the registry name the failure belongs to.
	Provider string
	// StatusCode is the provider's HTTP status, or 0 for local/transport errors.
	StatusCode int
	// Body is the provider response body, bounded for error messages.
	Body string
	// Err is the underlying cause, if any.
	Err error
}

// Error implements error.
func (e *ProviderError) Error() string {
	msg := "provider " + e.Provider
	if e.StatusCode != 0 {
		msg += fmt.Sprintf(": HTTP %d", e.StatusCode)
		if body := strings.TrimSpace(e.Body); body != "" {
			msg += ": " + truncateForError(body)
		}
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

// Unwrap exposes the underlying cause to errors.Is/As.
func (e *ProviderError) Unwrap() error { return e.Err }

// truncateForError bounds a provider body for error messages, marking cuts.
func truncateForError(body string) string {
	if len(body) <= maxErrorBody {
		return body
	}
	return body[:maxErrorBody] + "…(truncated)"
}
