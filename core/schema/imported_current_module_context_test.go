package schema

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestImportedModuleDefinitionKeepsCurrentLocalContext(t *testing.T) {
	for _, tc := range []struct {
		name           string
		exportCurrent  bool
		legacyProducer bool
		sameClient     bool
	}{
		{name: "definition-only"},
		{name: "current-value", exportCurrent: true},
		{name: "legacy-current-value", exportCurrent: true, legacyProducer: true},
		{name: "same-client-current-value", exportCurrent: true, sameClient: true},
		{name: "same-client-legacy-value", exportCurrent: true, legacyProducer: true, sameClient: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctxA, a, srvA, serverA := moduleDefinitionTestEnv(t, filepath.Join(t.TempDir(), "a.db"), "a", &moduleDefinitionTestResolver{})
			if tc.legacyProducer {
				dagql.Fields[*core.Query]{dagql.FuncWithDynamicInputs("currentModule", (&moduleSchema{}).currentModule, legacyCurrentModuleCacheKey)}.Install(srvA)
			}
			load := func(ctx context.Context, cache *dagql.Cache, srv *dagql.Server, session string, impl *definitionTypesSDK) dagql.ObjectResult[*core.Module] {
				t.Helper()
				in := definitionTestInputsFor(t, ctx, cache, srv, session, "source-"+session, digest.FromString("same implementation"), "runtime", "schema")
				source := in.source.Self().Clone()
				source.Kind = core.ModuleSourceKindLocal
				source.Local = &core.LocalModuleSource{ContextDirectoryPath: "/same/checkout"}
				source.SDK = &core.SDKConfig{Source: "scripted"}
				source.SDKImpl = impl
				src := attachDefinitionTestResult(t, ctx, cache, srv, session, "native-source-"+session, source)
				query, err := core.CurrentQuery(ctx)
				require.NoError(t, err)
				base, err := NewCoreSchemaBase(ctx, query.Server)
				require.NoError(t, err)
				mod := &core.Module{Source: dagql.NonNull(src), ContextSource: dagql.NonNull(src), NameField: "demo", OriginalName: "demo", SDKConfig: source.SDK, Deps: core.NewSchemaBuilder(query, []core.Mod{base.CoreMod("")})}
				loaded, err := (&moduleSourceSchema{}).runModuleDefInSDK(ctx, mod)
				require.NoError(t, err)
				return attachDefinitionTestResult(t, ctx, cache, srv, session, "operational-module-"+session, loaded)
			}
			implA := &definitionTypesSDK{}
			modA := load(ctxA, a, srvA, "a", implA)
			scopedA, err := core.ImplementationScopedModule(ctxA, modA)
			require.NoError(t, err)
			serverA.current.Store(&modA)
			var currentA dagql.ObjectResult[*core.CurrentModule]
			require.NoError(t, srvA.Select(ctxA, srvA.Root(), &currentA, dagql.Selector{Field: "currentModule"}))
			path, err := currentA.Self().Module.Self().Source.Value.Self().LocalContextDirectoryPath()
			require.NoError(t, err)
			require.Equal(t, "/same/checkout", path)
			roots := []dagql.AnyResult{scopedA}
			if tc.exportCurrent {
				roots = append(roots, currentA)
			}
			var bundle dagql.ValueBundle
			require.NoError(t, a.WithExportedValues(ctxA, dagql.ValueSelection{Roots: roots}, config.RefConfig{}, func(_ context.Context, values *dagql.ExportedValues) error {
				bundle = values.Bundle
				return nil
			}))
			ctxB, b, srvB, serverB := moduleDefinitionTestEnv(t, filepath.Join(t.TempDir(), "b.db"), "b", &moduleDefinitionTestResolver{})
			if tc.sameClient {
				ctxB = withClient(ctxB, "a", "b")
			}
			merged, err := b.MergeValues(ctxB, dagql.CloudCacheID, bundle)
			require.NoError(t, err)
			if tc.exportCurrent {
				roots := merged.Imported()
				require.Len(t, roots, 2)
				foreign, err := b.LoadResultByResultID(ctxB, "b", srvB, roots[1].ResultID)
				require.NoError(t, err)
				current, ok := foreign.(dagql.ObjectResult[*core.CurrentModule])
				require.True(t, ok)
				_, err = current.Self().Module.Self().Source.Value.Self().LocalContextDirectoryPath()
				require.ErrorIs(t, err, core.ErrForeignModuleContext, "imported host paths must remain unusable")
			}
			implB := &definitionTypesSDK{}
			modB := load(ctxB, b, srvB, "b", implB)
			require.Zero(t, implB.typesCalls.Load(), "B must reuse A's imported type definition")
			require.True(t, dagql.IsImportedResult(modB.Self().Definition.Value))
			_, err = modB.Self().Source.Value.Self().LocalContextDirectoryPath()
			require.NoError(t, err, "B freshly acquired its own operational module source")
			scopedB, err := core.ImplementationScopedModule(ctxB, modB)
			require.NoError(t, err)
			_, err = scopedB.Self().Source.Value.Self().LocalContextDirectoryPath()
			require.NoError(t, err, "implementation scoping must retain B's native source")
			serverB.current.Store(&modB)
			var currentB dagql.ObjectResult[*core.CurrentModule]
			require.NoError(t, srvB.Select(ctxB, srvB.Root(), &currentB, dagql.Selector{Field: "currentModule"}))
			path, err = currentB.Self().Module.Self().Source.Value.Self().LocalContextDirectoryPath()
			require.NoError(t, err, "an imported definition must not replace B's operational local context")
			require.Equal(t, "/same/checkout", path)
			var repeated dagql.ObjectResult[*core.CurrentModule]
			require.NoError(t, srvB.Select(ctxB, srvB.Root(), &repeated, dagql.Selector{Field: "currentModule"}))
			require.Equal(t, persistedID(t, b, currentB), persistedID(t, b, repeated), "same-engine reflection remains reusable")
		})
	}
}

func legacyCurrentModuleCacheKey(ctx context.Context, parent dagql.ObjectResult[*core.Query], args currentModuleArgs, req *dagql.CallRequest) error {
	if args.ImplementationScopedMod.Valid {
		return nil
	}
	mod, err := parent.Self().CurrentModule(ctx)
	if err != nil {
		return err
	}
	scoped, err := core.ImplementationScopedModule(ctx, mod)
	if err != nil {
		return err
	}
	id, err := scoped.ID()
	if err != nil {
		return err
	}
	return req.SetArgInput(ctx, "implementationScopedMod", dagql.NewID[*core.Module](id), false)
}
