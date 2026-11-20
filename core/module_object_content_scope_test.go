package core

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestModuleObjectContentDomain(t *testing.T) {
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	ctx := dagql.ContextWithCache(t.Context(), cache)
	defer func() { require.NoError(t, cache.Close(ctx)) }()
	server := newCoreDagqlServerForTest(t, &Query{})
	server.InstallObject(dagql.NewClass[*Module](server))

	module := func(version string) dagql.ObjectResult[*Module] {
		value := &Module{NameField: "same-module"}
		result, err := dagql.NewObjectResultForCall(value, server, &dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: version,
			Type:        dagql.NewResultCallType(value.Type()),
		})
		require.NoError(t, err)
		return result
	}
	first := module("module-version-one")
	second := module("module-version-two")
	content := func(owner dagql.ObjectResult[*Module], name, value string) digest.Digest {
		definition := NewObjectTypeDef(name, "", nil)
		object := &ModuleObject{Module: owner, TypeDef: definition, Fields: map[string]any{"value": value}}
		result, err := dagql.NewResultForCall(object, &dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "object-value",
			Type:        dagql.NewResultCallType(object.Type()),
		})
		require.NoError(t, err)
		collected := NewCollectedContent()
		require.NoError(t, (&ModuleObjectType{mod: owner, typeDef: definition}).CollectContent(ctx, result, collected))
		return collected.Digest()
	}

	t.Run("same type and defining module retains reuse", func(t *testing.T) {
		require.Equal(t, content(first, "Shared", "same"), content(first, "Shared", "same"))
	})
	t.Run("different type in one module is not equivalent", func(t *testing.T) {
		require.NotEqual(t, content(first, "First", "same"), content(first, "Second", "same"))
	})
	t.Run("same type in different module versions is not equivalent", func(t *testing.T) {
		require.NotEqual(t, content(first, "Shared", "same"), content(second, "Shared", "same"))
	})
	t.Run("changed fields are not equivalent", func(t *testing.T) {
		require.NotEqual(t, content(first, "Shared", "one"), content(first, "Shared", "two"))
	})
}

func TestModuleObjectContentUpgradeDoesNotServeLegacyEquivalence(t *testing.T) {
	for _, workspaceArgPresent := range []bool{true, false} {
		t.Run(fmt.Sprintf("workspace-argument-%t", workspaceArgPresent), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cache, err := dagql.NewCache(t.Context(), "", nil, nil)
				require.NoError(t, err)
				ctx := dagql.ContextWithCache(t.Context(), cache)
				defer func() { require.NoError(t, cache.Close(ctx)) }()
				server := newCoreDagqlServerForTest(t, &Query{})
				server.InstallObject(dagql.NewClass[*Module](server))
				installTypeDefTestClasses(server)
				owner := func(version string) dagql.ObjectResult[*Module] {
					mod := &Module{NameField: "same-module", Description: version}
					result, err := dagql.NewObjectResultForCall(mod, server, moduleObjectTestSyntheticCall(version, mod))
					require.NoError(t, err)
					return result
				}
				first, second := owner("version-one"), owner("version-two")
				definition := NewObjectTypeDef("Shared", "", nil)
				prototype := &ModuleObject{Module: first, TypeDef: definition}
				server.InstallObject(dagql.NewClass(server, dagql.ClassOpts[*ModuleObject]{Typed: prototype}))
				workspace := newTypeDefDetachedResult(t, server, "workspace", NewObjectTypeDef("Workspace", "", nil))
				workspaceType := newTypeDefDetachedResult(t, server, "workspace-type", (&TypeDef{}).WithObjectTypeDef(workspace))
				workspaceArg := newTypeDefDetachedResult(t, server, "workspace-arg", &FunctionArg{Name: "ws", TypeDef: workspaceType})
				fn := &ModuleFunction{
					mod:        first,
					metadata:   &Function{Args: dagql.ObjectResultArray[*FunctionArg]{workspaceArg}},
					returnType: &ModuleObjectType{mod: first, typeDef: definition},
				}
				frame := func(name string) *dagql.ResultCall { return moduleObjectTestSyntheticCall(name, prototype) }
				value := func(call *dagql.ResultCall, mod dagql.ObjectResult[*Module], legacy bool) dagql.AnyResult {
					obj := &ModuleObject{Module: mod, TypeDef: definition, Fields: map[string]any{"value": "same"}}
					result, err := dagql.NewResultForCall(obj, call)
					require.NoError(t, err)
					var content digest.Digest
					if legacy {
						fields := NewCollectedContent()
						require.NoError(t, fields.CollectKeyed("value", func() error { return fields.CollectJSONable("same") }))
						content = fields.Digest()
					} else {
						fields := NewCollectedContent()
						require.NoError(t, fn.returnType.CollectContent(ctx, result, fields))
						content = fields.Digest()
					}
					result, err = result.WithContentDigest(ctx, content)
					require.NoError(t, err)
					return result
				}
				if !workspaceArgPresent {
					fn.metadata.Args = nil
				}
				old := frame("original")
				_, err = cache.GetOrInitCall(ctx, "old-session", server, &dagql.CallRequest{ResultCall: old, TTL: 1}, func(context.Context) (dagql.AnyResult, error) { return value(old, first, true), nil })
				require.NoError(t, err)
				other := frame("other")
				_, err = cache.GetOrInitCall(ctx, "old-session", server, &dagql.CallRequest{ResultCall: other}, func(context.Context) (dagql.AnyResult, error) { return value(other, second, true), nil })
				require.NoError(t, err)
				time.Sleep(2 * time.Second)
				freshCalls := 0
				for range 2 {
					request := &dagql.CallRequest{ResultCall: frame("original")}
					for _, input := range fn.cacheImplicitInputs() {
						inputValue, err := input.Resolver(ctx, nil)
						require.NoError(t, err)
						require.NoError(t, request.SetImplicitInput(ctx, input.Name, inputValue))
					}
					result, err := cache.GetOrInitCall(ctx, "new-session", server, request, func(context.Context) (dagql.AnyResult, error) {
						freshCalls++
						return value(request.ResultCall, first, false), nil
					})
					require.NoError(t, err)
					object, ok := dagql.UnwrapAs[*ModuleObject](result)
					require.True(t, ok)
					require.Equal(t, "version-one", object.Module.Self().Description)
				}
				require.Equal(t, 1, freshCalls, "the repaired identity must run once and then reuse")
			})
		})
	}
}
