package gitserver

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/emirpasic/gods/trees/binaryheap"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/object/commitgraph"
)

type GitBrowserTree struct {
	Files []GitFile
	Dirs  []GitDir
}

type GitFile struct {
	Hash   string
	Path   string
	Name   string
	Mode   string
	Commit GitCommit
}

type GitDir struct {
	Name   string
	Commit GitCommit
}

func (gb *GitBrowser) browseTree() error {
	refCommit, _ := gb.Repo.CommitObject(*gb.RefHash)
	tree, err := refCommit.Tree()
	if err != nil {
		return caddyhttp.Error(503, err)
	}
	pageData := new(GitBrowserTree)
	var files []string
	var dirs []string
	isRoot := true
	if gb.PageArgs != "" {
		// We are not at the root of the repo. Add a ".." directory to navigate upwards in the tree
		// updir := path.Dir(gb.PageArgs)
		isRoot = false
		tree, err = tree.Tree(gb.PageArgs)
		if err != nil {
			return caddyhttp.Error(503, err)
		}

	}
	for _, entry := range tree.Entries {
		if entry.Mode.IsFile() {
			files = append(files, entry.Name)
		} else {
			// Directory
			dirs = append(dirs, entry.Name)
		}
	}
	// Sort files and dirs list in alphabetical order
	sort.Strings(files)
	sort.Strings(dirs)
	commitNodeIndex := commitgraph.NewObjectCommitNodeIndex(gb.Repo.Storer)
	commitNode, err := commitNodeIndex.Get(*gb.RefHash)
	if err != nil {
		return caddyhttp.Error(503, err)
	}
	// fmt.Println(pageArgs)
	fileRevs, err := getLastCommitForPaths(commitNode, gb.PageArgs, files)
	if err != nil {
		return caddyhttp.Error(503, err)
	}
	dirRevs, err := getLastCommitForPaths(commitNode, gb.PageArgs, dirs)
	if err != nil {
		return caddyhttp.Error(503, err)
	}

	for _, file := range files {
		rev := fileRevs[file]
		fileObj, err := rev.File(filepath.Join(gb.PageArgs, file))
		if err != nil {
			fmt.Printf("Couldn't find file: %s %v\n", gb.PageArgs+file, err)
		} else {
			msg := strings.TrimSpace(rev.Message)
			if len(msg) > 50 {
				rmsg := []rune(msg)
				msg = string(rmsg[:50]) + "..."
			}
			f := GitFile{
				Hash: fileObj.Hash.String(),
				Path: filepath.Join(gb.PageArgs, file),
				Name: file,
				Mode: fileObj.Mode.String(),
				Commit: GitCommit{
					Hash:      rev.Hash.String(),
					Committer: rev.Author.Name,
					Date:      rev.Committer.When.UTC().Format(dateFmt),
					Message:   msg,
				},
			}
			pageData.Files = append(pageData.Files, f)
		}
	}

	// Add a '..' dir first in line
	if !isRoot {
		pageData.Dirs = append(pageData.Dirs, GitDir{Name: ".."})
	}

	for _, dir := range dirs {
		rev := dirRevs[dir]
		d := GitDir{
			Name: dir,
			Commit: GitCommit{
				Hash:      rev.Hash.String(),
				Committer: rev.Author.Name,
				Date:      rev.Committer.When.UTC().Format(dateFmt),
				Message:   rev.Message,
			},
		}
		pageData.Dirs = append(pageData.Dirs, d)
	}

	gb.PageData = pageData

	return nil
}

type commitAndPaths struct {
	commit commitgraph.CommitNode
	// Paths that are still on the branch represented by commit
	paths []string
	// Set of hashes for the paths
	hashes map[string]plumbing.Hash
}

func getCommitTree(c commitgraph.CommitNode, treePath string) (*object.Tree, error) {
	tree, err := c.Tree()
	if err != nil {
		return nil, err
	}

	// Optimize deep traversals by focusing only on the specific tree
	if treePath != "" {
		tree, err = tree.Tree(treePath)
		if err != nil {
			return nil, err
		}
	}

	return tree, nil
}

// func getFullPath(treePath, path string) string {
// 	if treePath != "" {
// 		if path != "" {
// 			return treePath + "/" + path
// 		}
// 		return treePath
// 	}
// 	return path
// }

func getFileHashes(c commitgraph.CommitNode, treePath string, paths []string) (map[string]plumbing.Hash, error) {
	tree, err := getCommitTree(c, treePath)
	if err == object.ErrDirectoryNotFound {
		// The whole tree didn't exist, so return empty map
		return make(map[string]plumbing.Hash), nil
	}
	if err != nil {
		return nil, err
	}

	hashes := make(map[string]plumbing.Hash)
	for _, path := range paths {
		if path != "" {
			entry, err := tree.FindEntry(path)
			if err == nil {
				hashes[path] = entry.Hash
			}
		} else {
			hashes[path] = tree.Hash
		}
	}

	return hashes, nil
}

func getLastCommitForPaths(c commitgraph.CommitNode, treePath string, paths []string) (map[string]*object.Commit, error) {
	// We do a tree traversal with nodes sorted by commit time
	heap := binaryheap.NewWith(func(a, b interface{}) int {
		if a.(*commitAndPaths).commit.CommitTime().Before(b.(*commitAndPaths).commit.CommitTime()) {
			return 1
		}
		return -1
	})

	resultNodes := make(map[string]commitgraph.CommitNode)
	initialHashes, err := getFileHashes(c, treePath, paths)
	if err != nil {
		return nil, err
	}

	// Start search from the root commit and with full set of paths
	heap.Push(&commitAndPaths{c, paths, initialHashes})

	for {
		cIn, ok := heap.Pop()
		if !ok {
			break
		}
		current := cIn.(*commitAndPaths)

		// Load the parent commits for the one we are currently examining
		numParents := current.commit.NumParents()
		var parents []commitgraph.CommitNode
		for i := 0; i < numParents; i++ {
			parent, err := current.commit.ParentNode(i)
			if err != nil {
				break
			}
			parents = append(parents, parent)
		}

		// Examine the current commit and set of interesting paths
		pathUnchanged := make([]bool, len(current.paths))
		parentHashes := make([]map[string]plumbing.Hash, len(parents))
		for j, parent := range parents {
			parentHashes[j], err = getFileHashes(parent, treePath, current.paths)
			if err != nil {
				break
			}

			for i, path := range current.paths {
				if parentHashes[j][path] == current.hashes[path] {
					pathUnchanged[i] = true
				}
			}
		}

		var remainingPaths []string
		for i, path := range current.paths {
			// The results could already contain some newer change for the same path,
			// so don't override that and bail out on the file early.
			if resultNodes[path] == nil {
				if pathUnchanged[i] {
					// The path existed with the same hash in at least one parent so it could
					// not have been changed in this commit directly.
					remainingPaths = append(remainingPaths, path)
				} else {
					// There are few possible cases how can we get here:
					// - The path didn't exist in any parent, so it must have been created by
					//   this commit.
					// - The path did exist in the parent commit, but the hash of the file has
					//   changed.
					// - We are looking at a merge commit and the hash of the file doesn't
					//   match any of the hashes being merged. This is more common for directories,
					//   but it can also happen if a file is changed through conflict resolution.
					resultNodes[path] = current.commit
				}
			}
		}

		if len(remainingPaths) > 0 {
			// Add the parent nodes along with remaining paths to the heap for further
			// processing.
			for j, parent := range parents {
				// Combine remainingPath with paths available on the parent branch
				// and make union of them
				remainingPathsForParent := make([]string, 0, len(remainingPaths))
				newRemainingPaths := make([]string, 0, len(remainingPaths))
				for _, path := range remainingPaths {
					if parentHashes[j][path] == current.hashes[path] {
						remainingPathsForParent = append(remainingPathsForParent, path)
					} else {
						newRemainingPaths = append(newRemainingPaths, path)
					}
				}

				if remainingPathsForParent != nil {
					heap.Push(&commitAndPaths{parent, remainingPathsForParent, parentHashes[j]})
				}

				if len(newRemainingPaths) == 0 {
					break
				} else {
					remainingPaths = newRemainingPaths
				}
			}
		}
	}

	// Post-processing
	result := make(map[string]*object.Commit)
	for path, commitNode := range resultNodes {
		var err error
		result[path], err = commitNode.Commit()
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}
