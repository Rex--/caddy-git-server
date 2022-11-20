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

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"go.uber.org/zap"
)

//go:embed browse.html
var templateFile string

type GitBrowser struct {
	Name        string
	Tagline     string
	Description string
	Path        string
	Host        string
	CloneURL    string
	Now         string

	Branches []string
	Tags     []string

	// Some Values we extract out of the repo
	Readme string
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

	var templateStr string
	templateName := "default"
	if gsrv.Template != "" {
		userTemplate, err := os.ReadFile(gsrv.Template)
		if err != nil {
			templateStr = templateFile
			templateName = gsrv.Template
		} else {
			templateStr = string(userTemplate)
		}
	} else {
		templateStr = templateFile
	}
	// Load up our template
	browseTemplate, err := template.New("browse").Funcs(fm).Parse(templateStr)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

	// Create our template data object
	gb := GitBrowser{
		Name: strings.TrimSuffix(filepath.Base(repoPath), ".git"),
		Path: r.URL.Path + ".git",
		Host: r.Host,
		Now:  time.Now().UTC().Format(time.UnixDate),
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

	// Set our clone url
	cloneUrl := "https://" + r.Host + r.URL.Path
	if !strings.HasSuffix(cloneUrl, ".git") {
		cloneUrl += ".git"
	}
	gb.CloneURL = cloneUrl

	// Extract branches from repo
	branches, err := repo.Branches()
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}
	branches.ForEach(func(r *plumbing.Reference) error { gb.Branches = append(gb.Branches, r.String()); return nil })

	// Extract tags
	tags, err := repo.Tags()
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}
	tags.ForEach(func(r *plumbing.Reference) error { gb.Tags = append(gb.Tags, r.String()); return nil })

	gsrv.logger.Info("serving git browser",
		zap.String("query", r.URL.RawQuery),
		zap.String("request_path", r.URL.Path),
		zap.String("git_repo", repoPath),
		zap.String("template", templateName))

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
