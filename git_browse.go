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
	"github.com/russross/blackfriday/v2"
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

type GitBrowserPage interface{}

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
	PageArgs    string
	Root        string
	RefString   string

	PageData GitBrowserPage

	Repo    *git.Repository
	RefHash *plumbing.Hash

	// Static assets
	Assets StaticAssets
}

type StaticAssets struct {
	GitIcon string
}

var dateFmt string = "2006-01-02 15:04:05"

func (gsrv *GitServer) serveGitBrowser(repoPath string, w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	// We can assume the repo exists, so go ahead and open it
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

	// Setup function map
	fm := template.FuncMap{
		"split": strings.Split,
		"string": func(b []byte) string {
			return string(b)
		},
		"markdown": func(b []byte) template.HTML {
			marked := blackfriday.Run(b)
			return template.HTML(marked)
		},
		"dir": func(path string) string {
			return filepath.Dir(path)
		},
		"base": func(path string) string {
			return filepath.Base(path)
		},
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
	pageName, pageArgs, defined := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/"), pfx), "/"), "/")
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
		Name:     strings.TrimSuffix(filepath.Base(repoPath), ".git"),
		Path:     r.URL.Path,
		Page:     pageName,
		PageArgs: pageArgs,
		Host:     r.Host,
		Now:      time.Now().UTC().Format(time.UnixDate),
		Assets:   static_assets,
		Root:     pfx,

		Repo: repo,
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

	var refStr = "HEAD"
	if r.URL.Query().Has("ref") {
		refStr = r.URL.Query().Get("ref")
	} else if r.URL.Query().Has("branch") {
		refStr = "refs/heads/" + r.URL.Query().Get("branch")
	} else if r.URL.Query().Has("tag") {
		refStr = "refs/tags/" + r.URL.Query().Get("tag")
	}
	gb.RefString = refStr

	rev, err := repo.ResolveRevision(plumbing.Revision(refStr))
	if err != nil {
		return caddyhttp.Error(503, err)
	}
	gb.RefHash = rev

	gsrv.logger.Info("serving git browser",
		zap.String("request_path", r.URL.Path),
		zap.String("git_repo", repoPath),
		zap.String("query", r.URL.RawQuery),
		zap.String("template_base", templateBaseName),
		zap.String("template_page", templatePageName),
	)

	if pageName == "log" {
		gb.browseLog()
	} else if pageName == "tree" {
		gb.browseTree()
	} else if pageName == "blob" {
		gb.browseBlob()
	} else if pageName == "home" {
		gb.browseHome()
	} else if pageName == "raw" {
		gb.browseRaw()
		// we just serve the raw file content after setting the mimetype
		gb.serveRaw(w)
		return nil
	}

	// Set content type header
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Write to connection
	err = browseTemplate.Execute(w, gb)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}
	// fmt.Fprintf(w, "<html><h1>%s</html></h1>", refString)

	return nil
}
