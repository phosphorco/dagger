package telemetry_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/engine/telemetry"
	"github.com/stretchr/testify/require"
)

func TestGitCheckoutIdentitySurvivesEditsAndSubdirectories(t *testing.T) {
	repo := setupRepo(t)
	identity, ok := telemetry.NewLabels(nil, nil, nil).WithGitLabels(repo).Get("dagger.io/git.worktree")
	require.True(t, ok, "native placement needs an identity for this checkout")
	require.NotEmpty(t, identity)
	subdir := filepath.Join(repo, "nested")
	require.NoError(t, os.Mkdir(subdir, 0700))
	fromSubdir, ok := telemetry.NewLabels(nil, nil, nil).WithGitLabels(subdir).Get("dagger.io/git.worktree")
	require.True(t, ok)
	require.Equal(t, identity, fromSubdir)
	run(t, "git", "-C", repo, "commit", "--allow-empty", "-m", "next revision")
	afterCommit, ok := telemetry.NewLabels(nil, nil, nil).WithGitLabels(repo).Get("dagger.io/git.worktree")
	require.True(t, ok)
	require.Equal(t, identity, afterCommit)
	other := setupRepo(t)
	otherIdentity, ok := telemetry.NewLabels(nil, nil, nil).WithGitLabels(other).Get("dagger.io/git.worktree")
	require.True(t, ok)
	require.NotEqual(t, identity, otherIdentity, "separate checkouts must not overwrite each other's last worker")
}

func TestGitCheckoutIdentityResolvesSymlinks(t *testing.T) {
	repo := setupRepo(t)
	alias := filepath.Join(t.TempDir(), "checkout")
	require.NoError(t, os.Symlink(repo, alias))
	identity, ok := telemetry.NewLabels(nil, nil, nil).WithGitLabels(repo).Get("dagger.io/git.worktree")
	require.True(t, ok)
	fromAlias, ok := telemetry.NewLabels(nil, nil, nil).WithGitLabels(alias).Get("dagger.io/git.worktree")
	require.True(t, ok)
	require.Equal(t, identity, fromAlias)
}

func TestGitLinkedWorktreesHaveDistinctCheckoutIdentities(t *testing.T) {
	repo := setupRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	run(t, "git", "-C", repo, "worktree", "add", "--detach", linked, "HEAD")
	identity, ok := telemetry.NewLabels(nil, nil, nil).WithGitLabels(repo).Get("dagger.io/git.worktree")
	require.True(t, ok)
	linkedIdentity, ok := telemetry.NewLabels(nil, nil, nil).WithGitLabels(linked).Get("dagger.io/git.worktree")
	require.True(t, ok)
	require.NotEqual(t, identity, linkedIdentity)
}

func TestNonGitDirectoryDoesNotInventCheckoutIdentity(t *testing.T) {
	_, ok := telemetry.NewLabels(nil, nil, nil).WithGitLabels(t.TempDir()).Get("dagger.io/git.worktree")
	require.False(t, ok)
}
