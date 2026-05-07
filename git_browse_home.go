package gitserver

import (
	"bufio"
	"io"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

type GitBrowserHome struct {
	// Branches []GitRef
	// Tags     []GitRef

	Updated   string
	Committer string
	Message   string

	Readme     []byte
	ReadmeType string
}

func (gb *GitBrowser) browseHome() error {
	// Create new page to hold template data
	pageData := new(GitBrowserHome)
	// // Extract branches from repo
	// branches, err := gb.Repo.Branches()
	// if err != nil {
	// 	return caddyhttp.Error(http.StatusInternalServerError, err)
	// }
	// branches.ForEach(func(r *plumbing.Reference) error {
	// 	b := GitRef{
	// 		Hash: r.Hash().String(),
	// 		Type: r.Type().String(),
	// 		Name: r.Name().Short(),
	// 	}
	// 	pageData.Branches = append(pageData.Branches, b)
	// 	return nil
	// })

	// // Extract tags
	// tags, err := gb.Repo.Tags()
	// if err != nil {
	// 	return caddyhttp.Error(http.StatusInternalServerError, err)
	// }
	// tags.ForEach(func(r *plumbing.Reference) error {
	// 	t := GitRef{
	// 		Hash: r.Hash().String(),
	// 		Type: r.Type().String(),
	// 		Name: r.Name().Short(),
	// 	}
	// 	pageData.Tags = append(pageData.Tags, t)
	// 	return nil
	// })

	// Get info about the last commit
	refCommit, err := gb.Repo.CommitObject(*gb.RefHash)
	if err != nil {
		return caddyhttp.Error(503, err)
	}
	pageData.Updated = refCommit.Committer.When.UTC().Format(time.UnixDate)
	pageData.Committer = refCommit.Author.String()
	if len(refCommit.Message) > 50 {
		rmsg := []rune(refCommit.Message)
		pageData.Message = string(rmsg[:50]) + "..."
	} else {
		pageData.Message = refCommit.Message
	}

	// Look for README contained in root of repo
	tree, err := refCommit.Tree()
	if err != nil {
		return caddyhttp.Error(503, err)
	}
	filetype := "text"
	file, err := tree.File("README.md") // Look for markdown readme
	if err != nil {
		file, err = tree.File("README.txt") // Look for plaintext readme
		if err != nil {
			file, err = tree.File("README") // Look for plaintext readme without extension
			if err != nil {
				filetype = "none"
			}
		}
	} else {
		filetype = "markdown"
	}

	pageData.ReadmeType = filetype

	if filetype == "markdown" || filetype == "text" {
		strBuilder := new(strings.Builder)
		reader, err := file.Blob.Reader()
		if err != nil {
			return caddyhttp.Error(503, err)
		}
		blobReader := bufio.NewReader(reader)
		_, err = io.Copy(strBuilder, blobReader)
		if err != nil {
			return caddyhttp.Error(503, err)
		}
		// if filetype == "markdown" && strings.Contains(strBuilder.String(), "![") {
		// 	pageData.Readme = []byte(strings.ReplaceAll(strBuilder.String(), "](", "](/"+gb.Root+"/raw/"))
		// } else {
		// }
		pageData.Readme = []byte(strBuilder.String())
	}

	gb.PageData = pageData
	return nil
}
