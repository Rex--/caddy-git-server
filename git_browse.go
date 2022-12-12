package gitserver

import (
	_ "embed"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/emirpasic/gods/trees/binaryheap"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/object/commitgraph"
	"go.uber.org/zap"
)

// Default Page Templates
//

//go:embed templates/base.html
var template_base string

//go:embed templates/404.html
var template_page_404 string

//go:embed templates/home.html
var template_page_home string

//go:embed templates/blob.html
var template_page_blob string

//go:embed templates/tree.html
var template_page_tree string

//go:embed templates/log.html
var template_page_log string

// Static assets
//
//go:embed static/git-icon.b64
var static_gitIcon string

var template_pages = map[string]*string{
	"home": &template_page_home,
	"blob": &template_page_blob,
	"tree": &template_page_tree,
	"log":  &template_page_log,
}

var static_assets = StaticAssets{
	GitIcon: static_gitIcon,
}

type GitBrowser struct {
	Name        string
	Tagline     string
	Description string
	Path        string
	Host        string
	CloneURL    string
	Now         string
	Scheme      string
	Page        string
	Root        string
	Updated     string
	Committer   string

	Branches []GitRef
	Tags     []GitRef

	Commits []GitCommit

	Files []GitFile

	// Static assets
	Assets StaticAssets
}

type GitRef struct {
	// SHA1 hash
	Hash string
	// Ref type string, either 'refs/heads' or 'refs/tags
	Type string
	// Name of branch or tag
	Name string
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

type GitFile struct {
	Name   string
	Mode   string
	Commit GitCommit
}

type StaticAssets struct {
	GitIcon string
}

func (gsrv *GitServer) serveGitBrowser(repoPath string, w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	// We can assume the repo exists, so go ahead and open it
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

	// Setup function map
	fm := template.FuncMap{
		"split": strings.Split,
	}

	// Decide which base template to use (default embedded or user defined)
	// User template must be named "base.html" and be in the template_dir
	templateBaseStr := &template_base
	templateBaseName := "default"
	if gsrv.TemplateDir != "" {
		tbn := filepath.Join(gsrv.TemplateDir, "base.html")
		userBase, err := os.ReadFile(tbn)
		if err == nil {
			// Convert the read file into a string and set the new filename
			user_template_base := string(userBase)
			templateBaseStr = &user_template_base
			templateBaseName = tbn
		}
	}
	// Load up our base template
	browseTemplate, err := template.New("browse").Funcs(fm).Parse(*templateBaseStr)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

	// Decide which page to load and read template file if necessary
	// Page is determined by the path segment following the repository.
	// Any path after that is path arguments, currently only the reference
	root := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer).ReplaceAll(gsrv.Root, ".")
	pfx := strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(repoPath, root), ".git"), "/")
	pageName, _, defined := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/"), pfx), "/"), "/")
	if !defined && pageName == "" {
		pageName = "home"
	}
	// fmt.Println("looking for page", pageName)
	templatePageStr := template_pages[pageName]
	templatePageName := "default-" + pageName
	if gsrv.TemplateDir != "" {
		tpn := filepath.Join(gsrv.TemplateDir, pageName+".html")
		userPage, err := os.ReadFile(tpn)
		if err == nil {
			user_template_page := string(userPage)
			templatePageStr = &user_template_page
			templatePageName = tpn
		}
	}

	// If we couldn't find a page template, use the 404 page
	if templatePageStr == nil {
		templatePageStr = &template_page_404
		templatePageName = "default-404"
		if gsrv.TemplateDir != "" {
			tpn := filepath.Join(gsrv.TemplateDir, "404.html")
			user404, err := os.ReadFile(tpn)
			if err == nil {
				// Use user 404 page if one is found
				user_template_page := string(user404)
				templatePageStr = &user_template_page
				templatePageName = tpn
			}
		}
	}

	// Load up our page template
	browseTemplate.Parse(*templatePageStr)

	// Create our template data object
	gb := GitBrowser{
		Name:   strings.TrimSuffix(filepath.Base(repoPath), ".git"),
		Path:   r.URL.Path,
		Page:   pageName,
		Host:   r.Host,
		Now:    time.Now().UTC().Format(time.UnixDate),
		Assets: static_assets,
		Root:   pfx,
	}

	// Open the description file
	file, err := os.Open(filepath.Join(repoPath, "description"))
	if err != nil {
		return err
	}
	defer file.Close()

	// Read the full description file (keep it short)
	descBytes, err := io.ReadAll(file)
	if err != nil {
		// No description file?
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

	// Get first line as tagline, rest of file is the long description
	gb.Tagline, gb.Description, _ = strings.Cut(string(descBytes), "\n")

	// Set the scheme if it is empty. This is for generating a proper clone url
	if r.URL.Scheme == "" {
		if r.TLS == nil {
			r.URL.Scheme = "http"
		} else {
			r.URL.Scheme = "https"
		}
	}

	// Construct the clone url
	cloneUrl := r.URL.Scheme + "://" + r.Host + "/" + pfx + ".git"
	gb.CloneURL = cloneUrl

	// Extract branches from repo
	branches, err := repo.Branches()
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}
	branches.ForEach(func(r *plumbing.Reference) error {
		b := GitRef{
			Hash: r.Hash().String(),
			Type: r.Type().String(),
			Name: r.Name().Short(),
		}
		gb.Branches = append(gb.Branches, b)
		return nil
	})

	// Extract tags
	tags, err := repo.Tags()
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}
	tags.ForEach(func(r *plumbing.Reference) error {
		t := GitRef{
			Hash: r.Hash().String(),
			Type: r.Type().String(),
			Name: r.Name().Short(),
		}
		gb.Tags = append(gb.Tags, t)
		return nil
	})

	var refStr = "HEAD"
	if r.URL.Query().Has("ref") {
		refStr = r.URL.Query().Get("ref")
	} else if r.URL.Query().Has("branch") {
		refStr = "refs/heads/" + r.URL.Query().Get("branch")
	} else if r.URL.Query().Has("tag") {
		refStr = "refs/tags/" + r.URL.Query().Get("tag")
	}

	rev, err := repo.ResolveRevision(plumbing.Revision(refStr))
	if err != nil {
		return caddyhttp.Error(503, err)
	}

	if pageName == "log" {
		// Extract commits if needed
		commits, _ := repo.Log(&git.LogOptions{From: *rev})
		commits.ForEach(func(c *object.Commit) error {
			commit := GitCommit{
				Hash:      c.Hash.String(),
				Committer: c.Author.String(),
				Message:   c.Message,
				Date:      c.Committer.When.UTC().Format("2006-01-02 03:04:05 PM"),
			}
			gb.Commits = append(gb.Commits, commit)
			return nil
		})

	} else if pageName == "tree" {
		refCommit, _ := repo.CommitObject(*rev)
		tree, _ := refCommit.Tree()
		var paths []string
		for _, entry := range tree.Entries {
			paths = append(paths, entry.Name)
		}
		commitNodeIndex := commitgraph.NewObjectCommitNodeIndex(repo.Storer)
		commitNode, err := commitNodeIndex.Get(*rev)
		if err != nil {
			return caddyhttp.Error(503, err)
		}
		revs, _ := getLastCommitForPaths(commitNode, "", paths)

		for path, rev := range revs {
			fileObj, err := rev.File(path)
			var f GitFile
			if err != nil {
				// fmt.Printf("Couldn't find file: %s\n", path)
				// Directory ?
				f = GitFile{
					Name: path,
					Mode: "dir",
					Commit: GitCommit{
						Hash:      rev.Hash.String(),
						Committer: rev.Author.Name,
						Date:      rev.Committer.When.UTC().Format("2006-01-02 03:04:05 PM"),
						Message:   rev.Message,
					},
				}
			} else {

				f = GitFile{
					Name: fileObj.Name,
					Mode: fileObj.Mode.String(),
					Commit: GitCommit{
						Hash:      rev.Hash.String(),
						Committer: rev.Author.Name,
						Date:      rev.Committer.When.UTC().Format("2006-01-02 03:04:05 PM"),
						Message:   rev.Message,
					},
				}
			}
			gb.Files = append(gb.Files, f)
		}
	} else if pageName == "home" {
		refCommit, err := repo.CommitObject(*rev)
		if err != nil {
			return caddyhttp.Error(503, err)
		}
		gb.Updated = refCommit.Committer.When.UTC().Format("2006-01-02 03:04:05 PM")
		gb.Committer = refCommit.Author.String()
	}

	gsrv.logger.Info("serving git browser",
		zap.String("request_path", r.URL.Path),
		zap.String("git_repo", repoPath),
		zap.String("query", r.URL.RawQuery),
		zap.String("template_base", templateBaseName),
		zap.String("template_page", templatePageName),
	)

	// Fun with headers
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Write to connection
	err = browseTemplate.Execute(w, gb)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}
	// fmt.Fprintf(w, "<html><h1>%s</html></h1>", refString)

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
