package gitserver

import (
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type GitBrowserDiff struct {
	Diff string
}

func (gb *GitBrowser) browseDiff() error {
	pageData := new(GitBrowserDiff)

	commit1, err := gb.Repo.CommitObject(*gb.RefHash)
	if err != nil {
		return caddyhttp.Error(503, err)
	}

	println("commit1")

	commit2, err := commit1.Parent(0)
	if err != nil {
		return caddyhttp.Error(503, err)
	}

	println("commit2")

	tree1, err := commit1.Tree()
	if err != nil {
		return caddyhttp.Error(503, err)
	}

	println("tree1")

	tree2, err := commit2.Tree()
	if err != nil {
		return caddyhttp.Error(503, err)
	}

	println("tree2")

	changes, err := object.DiffTree(tree1, tree2)
	if err != nil {
		return caddyhttp.Error(503, err)
	}

	patch, err := changes.Patch()
	if err != nil {
		return caddyhttp.Error(503, err)
	}

	// println("Diff:", patch.String())
	pageData.Diff = patch.String()

	gb.PageData = pageData
	return nil
}
