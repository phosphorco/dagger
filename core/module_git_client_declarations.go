package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"resenje.org/singleflight"
)

type gitClientDeclarationKey struct {
	source  uint64
	kind    ModuleSourceKind
	foreign bool
	host    string
	epoch   string
}

type GitClientDeclarations struct {
	mu      sync.Mutex
	values  map[gitClientDeclarationKey]string
	flights singleflight.Group[gitClientDeclarationKey, string]
}

func NewGitClientDeclarations() *GitClientDeclarations { return new(GitClientDeclarations) }

func (m *GitClientDeclarations) get(ctx context.Context, key gitClientDeclarationKey, load func(context.Context) (string, error)) (string, error) {
	if m == nil {
		return load(ctx)
	}
	m.mu.Lock()
	value, ok := m.values[key]
	m.mu.Unlock()
	if ok {
		return value, nil
	}
	value, _, err := m.flights.Do(ctx, key, func(ctx context.Context) (string, error) {
		m.mu.Lock()
		value, ok := m.values[key]
		m.mu.Unlock()
		if ok {
			return value, nil
		}
		value, err := load(ctx)
		if err != nil {
			return "", err
		}
		m.mu.Lock()
		if m.values == nil {
			m.values = make(map[gitClientDeclarationKey]string)
		}
		m.values[key] = value
		m.mu.Unlock()
		return value, nil
	})
	return value, err
}

func moduleGitClientDeclarationKey(ctx context.Context, source dagql.ObjectResult[*ModuleSource]) (gitClientDeclarationKey, error) {
	id, err := source.ID()
	if err != nil {
		return gitClientDeclarationKey{}, err
	}
	if id == nil || id.EngineResultID() == 0 {
		return gitClientDeclarationKey{}, fmt.Errorf("module source has no attached identity")
	}
	src := source.Self()
	key := gitClientDeclarationKey{source: id.EngineResultID(), kind: src.Kind}
	if src.Kind != ModuleSourceKindLocal || src.Local == nil {
		return key, nil
	}
	key.foreign = src.Local.Foreign
	if key.foreign {
		return key, nil
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return key, err
	}
	host, err := query.ModuleParentHostClientMetadata(ctx)
	if errors.Is(err, ErrNoCurrentModule) {
		host, err = query.NonModuleParentClientMetadata(ctx)
	}
	if err != nil {
		return key, err
	}
	if host == nil {
		return key, fmt.Errorf("module source has no caller metadata")
	}
	key.host = host.ClientID
	key.epoch, err = WorkspaceReadEpoch(engine.ContextWithClientMetadata(ctx, host))
	return key, err
}
