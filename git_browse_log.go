package gitserver

import (
	"sort"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type GitBrowserLog struct {
	Commits []GitCommit
}

type GitCommit struct {
	// SHA1 commit hash
	Hash string
	// Committer of commit
	Committer string
	// Commit message
	Message string
	// Creation date (done by Author)
	Date string
}

func (gb *GitBrowser) browseLog() error {
	pageData := new(GitBrowserLog)
	// Extract commits if needed
	commits, _ := gb.Repo.Log(&git.LogOptions{From: *gb.RefHash})
	var unsortedCommits = make(map[int64]GitCommit)
	commits.ForEach(func(c *object.Commit) error {
		commit := GitCommit{
			Hash:      c.Hash.String(),
			Committer: c.Author.String(),
			Message:   c.Message,
			Date:      c.Committer.When.UTC().Format(dateFmt),
		}
		unsortedCommits[c.Committer.When.Unix()] = commit
		return nil
	})

	keys := make([]int, 0, len(unsortedCommits))
	for k := range unsortedCommits {
		keys = append(keys, int(k))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(keys)))

	for _, key := range keys {
		pageData.Commits = append(pageData.Commits, unsortedCommits[int64(key)])
	}

	gb.PageData = pageData

	return nil
}
