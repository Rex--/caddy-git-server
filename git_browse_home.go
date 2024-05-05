package gitserver

import (
	"net/http"
	"time"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/go-git/go-git/v5/plumbing"
)

type GitBrowserHome struct {
	Branches []GitRef
	Tags     []GitRef

	Updated   string
	Committer string
}

type GitRef struct {
	// SHA1 hash
	Hash string
	// Ref type string, either 'refs/heads' or 'refs/tags
	Type string
	// Name of branch or tag
	Name string
}

func (gb *GitBrowser) browseHome() error {
	// Create new page to hold template data
	pageData := new(GitBrowserHome)
	// Extract branches from repo
	branches, err := gb.Repo.Branches()
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}
	branches.ForEach(func(r *plumbing.Reference) error {
		b := GitRef{
			Hash: r.Hash().String(),
			Type: r.Type().String(),
			Name: r.Name().Short(),
		}
		pageData.Branches = append(pageData.Branches, b)
		return nil
	})

	// Extract tags
	tags, err := gb.Repo.Tags()
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}
	tags.ForEach(func(r *plumbing.Reference) error {
		t := GitRef{
			Hash: r.Hash().String(),
			Type: r.Type().String(),
			Name: r.Name().Short(),
		}
		pageData.Tags = append(pageData.Tags, t)
		return nil
	})

	// Get info about the last commit
	refCommit, err := gb.Repo.CommitObject(*gb.RefHash)
	if err != nil {
		return caddyhttp.Error(503, err)
	}
	pageData.Updated = refCommit.Committer.When.UTC().Format(time.UnixDate)
	pageData.Committer = refCommit.Author.String()

	gb.PageData = pageData
	return nil
}
