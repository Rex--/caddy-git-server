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
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
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
	//Author of commit
	Author string
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

	if pageName == "log" {
		// Extract commits if needed
		ref, err := repo.Head()
		if err == nil {
			commits, _ := repo.Log(&git.LogOptions{From: ref.Hash()})
			commits.ForEach(func(c *object.Commit) error {
				commit := GitCommit{
					Hash:      c.Hash.String(),
					Author:    c.Author.String(),
					Committer: c.Committer.String(),
					Message:   c.Message,
					Date:      c.Author.When.String(),
				}
				gb.Commits = append(gb.Commits, commit)
				return nil
			})
		}

	} else if pageName == "tree" {
		// Get list of files if needed
		ref, err := repo.Head()
		if err == nil {
			refCommit, _ := repo.CommitObject(ref.Hash())
			tree, _ := refCommit.Tree()
			for _, entry := range tree.Entries {
				f := GitFile{
					Name:   entry.Name,
					Mode:   entry.Mode.String(),
					Commit: GitCommit{Message: "Initial Commit - Added all files."},
				}
				gb.Files = append(gb.Files, f)
			}
		}
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
