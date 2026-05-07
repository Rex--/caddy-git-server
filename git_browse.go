package gitserver

import (
	"embed"
	_ "embed"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/go-git/go-git/v5"
	"github.com/h2non/filetype"
	"github.com/russross/blackfriday/v2"

	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"

	"go.uber.org/zap"
)

// Default Page Templates
//
//go:embed templates
var template_dir embed.FS

// Static assets
//
//go:embed assets
var asset_dir embed.FS

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
	Query       template.URL

	ListedDefault bool

	PageData GitBrowserPage

	Repo     *git.Repository
	RefHash  *plumbing.Hash
	Branches []GitRef
	Tags     []GitRef
}

type GitRef struct {
	// SHA1 hash
	Hash string
	// Ref type string, either 'refs/heads' or 'refs/tags
	Type string
	// Name of branch or tag
	Name string
}

type GitBrowserRoot struct {
	GitBrowserPage
	Repositories map[string]string
}

type GitBrowserAssetFile interface {
	Close()
	Read(buf []byte)
}

var dateFmt string = "2006-01-02" // 15:04:05"

func (gsrv *GitServer) ServeBrowser(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	gsrv.logger.Info("handling web browser",
		// zap.String("repo_path", repoPath),
		zap.String("req_path", r.URL.Path))

	// Check for root path
	if r.RequestURI == "/" {
		println("Handle root")
		repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
		root := repl.ReplaceAll(gsrv.Root, ".")
		gsrv.updateRepositories(root)
		return gsrv.serveRootBrowser(root, w, r, next)
	}

	// Handle embedded assets /assets/*
	if strings.HasPrefix(r.RequestURI, "/assets/") {
		asset := strings.TrimPrefix(r.RequestURI, "/assets/")
		println("Handling asset::::", asset)
		return gsrv.serveBrowseAsset(asset, w, r, next)
	}

	// Redirect /<repo>.git to /<repo>
	requestPath := strings.TrimSuffix(r.URL.Path, "/")
	if strings.HasSuffix(requestPath, ".git") {
		println("Handle redirect")
		http.Redirect(w, r, strings.TrimSuffix(requestPath, ".git"), http.StatusPermanentRedirect)
		return nil
	}

	// Get repo path on disk
	repoPath, err := gsrv.getRepoPath(r)
	if err != nil {
		gsrv.logger.Error("repository does not exist", zap.String("url", r.URL.Path))
		return next.ServeHTTP(w, r)
	}

	// Pass it on to the browse handler
	return gsrv.serveGitBrowser(repoPath, w, r, next)
}

func (gsrv *GitServer) serveRootBrowser(root string, w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	templateRoot, err := getTemplateStr("root.html", gsrv.TemplateDir)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

	rootTemplate, err := template.New("root").Parse(templateRoot)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

	gb := GitBrowser{
		Host: r.Host,
	}

	pageData := new(GitBrowserRoot)
	pageData.Repositories = make(map[string]string)
	for _, repoName := range gsrv.repositories {
		cfg_file, err := os.Open(filepath.Join(root, repoName+".git", "config"))
		if err != nil {
			println("Couldn't open config file:", err.Error())
			cfg_file.Close()
			continue
		}
		cfg, err := config.ReadConfig(cfg_file)
		if err != nil {
			println("Error reading config file:", err.Error())
			cfg_file.Close()
			continue
		}
		cfg_file.Close()

		// if cfg.Raw.HasSection("caddy-git-server") {
		listed := cfg.Raw.Section("caddy-git-server").Option("listed")
		if listed == "true" || (gsrv.ListedDefault && (listed != "false")) {
			desc := ""
			desc_bytes, err := os.ReadFile(filepath.Join(root, repoName+".git", "description"))
			if err == nil {
				desc = string(desc_bytes)
			}
			pageData.Repositories[repoName] = desc
		}
		// }
	}
	gb.PageData = pageData

	// Set content type header
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	err = rootTemplate.Execute(w, gb)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

	return nil
}

func (gsrv *GitServer) serveGitBrowser(repoPath string, w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	println("git browser")
	// We can assume the repo exists, so go ahead and open it
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

	// Setup function map
	fm := template.FuncMap{
		"split": strings.Split,
		// "cut":   strings.Cut,
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
		"sub": func(a, b int) int {
			return a - b
		},
	}

	// Decide which base template to use (default embedded or user defined)
	// User template must be named "base.html" and be in the template_dir
	templateBase, err := getTemplateStr("base.html", gsrv.TemplateDir)
	if err != nil {
		println("err  1")
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

	println("0.5")

	// Load up our base template .Funcs(fm)
	browseTemplate, err := template.New("browse").Funcs(fm).Parse(templateBase)
	println("yes")
	if err != nil {
		println("err2")
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

	println("1")

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
	templatePage, err := getTemplateStr(pageName+".html", gsrv.TemplateDir)
	if err != nil {
		templatePage, err = getTemplateStr("404.html", gsrv.TemplateDir)
		// templatePageName = "default-404"
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, err)
		}
	}

	// Load up our page template
	browseTemplate.Parse(templatePage)

	// Create our template data object
	gb := GitBrowser{
		Name:     strings.TrimSuffix(filepath.Base(repoPath), ".git"),
		Path:     r.URL.Path,
		Page:     pageName,
		PageArgs: pageArgs,
		Host:     r.Host,
		Now:      time.Now().UTC().Format(time.UnixDate),
		Root:     pfx,
		Query:    template.URL(r.URL.RawQuery),

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
	// If description file has the default content, display nothing.
	gb.Tagline, gb.Description, _ = strings.Cut(string(descBytes), "\n")
	if gb.Tagline == "Unnamed repository; edit this file 'description' to name the repository." {
		gb.Tagline = ""
	}

	println("2")

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
		gb.Branches = append(gb.Branches, b)
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
		gb.Tags = append(gb.Tags, t)
		return nil
	})

	println("3")

	gsrv.logger.Info("serving git browser",
		zap.String("request_path", r.URL.Path),
		zap.String("git_repo", repoPath),
		zap.String("query", r.URL.RawQuery),
		zap.String("template", pageName+".html"),
		// zap.String("template_base", templateBaseName),
		// zap.String("template_page", templatePageName),
	)

	if pageName == "log" {
		gb.browseLog()
	} else if pageName == "tree" {
		gb.browseTree()
	} else if pageName == "blob" {
		gb.browseBlob()
	} else if pageName == "home" {
		gb.browseHome()
	} else if pageName == "diff" {
		println("diff")
		gb.browseDiff()
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

func (gsrv *GitServer) serveBrowseAsset(asset string, w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	asset_file, err := getAssetFile(asset, gsrv.AssetDir)
	println("serveBrowseAsset")
	if err != nil {
		gsrv.logger.Info("unable to find asset file", zap.String("asset_file", asset))
		return next.ServeHTTP(w, r)
	}

	var asset_mime string

	fileext := filepath.Ext(asset)
	if fileext == ".js" {
		asset_mime = "application/javascript"
	} else if fileext == ".css" {
		asset_mime = "text/css"
	} else if fileext == ".png" {
		asset_mime = "image/png"
	} else {
		// Read file header into buffer to determine type
		fileHead := make([]byte, 261)
		asset_file.Read(fileHead)
		fileType, err := filetype.Match(fileHead)
		if err != nil {
			fileType.MIME.Type = "text/plain"
		}
		asset_mime = fileType.MIME.Value
		// Reopen the asset file to seek to beginning
		asset_file.Close()
		asset_file, err = getAssetFile(asset, gsrv.AssetDir)
		if err != nil {
			println("fail to reopen file")
			return next.ServeHTTP(w, r)
		}
	}

	println("Asset MIMEType:", asset_mime)

	w.Header().Set("Content-Type", asset_mime)

	io.Copy(w, asset_file)
	asset_file.Close()
	return nil
}

func getAssetFile(asset string, user_asset_dir string) (fs.File, error) {

	// Try to open asset from asset_dir first
	if user_asset_dir != "" {
		user_asset_file, err := os.Open(filepath.Join(user_asset_dir, asset))
		if err == nil {
			return user_asset_file, nil
		}
	}

	println("Using builtin asset")

	asset_file, err := asset_dir.Open(filepath.Join("assets", asset))
	if err != nil {
		println("fail to open builtin asset")
		return nil, err
	}

	println("success to open builtin asset")

	return asset_file, nil
}

func getTemplateStr(template string, user_template_dir string) (string, error) {

	if user_template_dir != "" {
		user_template, err := os.ReadFile(filepath.Join(user_template_dir, template))
		if err == nil {
			// Return the user template
			println("using user template")
			return string(user_template), nil
		}
	}

	println("using embedded template")

	default_template, err := template_dir.ReadFile(filepath.Join("templates", template))
	if err != nil {
		println("fail to use builtin template")
		return "", caddyhttp.Error(http.StatusInternalServerError, err)
	}
	return string(default_template), nil
}
