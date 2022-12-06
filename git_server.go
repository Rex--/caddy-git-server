package gitserver

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/fileserver"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(GitServer{})
	httpcaddyfile.RegisterHandlerDirective("git_server", parseCaddyfile)
}

type GitServer struct {
	// Git http protocol to use: 'dumb' or 'smart' or 'both' (default)
	// Note this doesn't actually do anything currently, only the dumb protocol is implemented.
	Protocol string `json:"protocol,omitempty"`

	// Path to directory containing bare git repos (<repo>.git)
	Root string `json:"root,omitempty"`

	// Enable repo browser
	Browse      bool   `json:"browse,omitempty"`
	TemplateDir string `json:"template_dir,omitempty"`

	// If IgnorePrefix is defined we strip it from the URL path
	IgnorePrefix string `json:"ignore_prefix,omitempty"`

	// Mirror a git repo
	// Mirror        bool `json:"mirror,omitempty"`
	// MirrorRemotes []string

	// File server module that serves static git files
	// FileServerRaw json.RawMessage        `json:"file_server,omitempty" caddy:"namespace=http.handlers inline_key=handler"`
	FileServer *fileserver.FileServer `json:"-"`

	// This is a list of relative paths to repositories in the root directory.
	// If set, the IgnorePrefix is stripped
	repositories             []string
	repositoriesLastModified time.Time

	logger *zap.Logger
}

// CaddyModule returns the Caddy module information.
func (GitServer) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.git_server",
		New: func() caddy.Module { return new(GitServer) },
	}
}

// Unmarshal caddyfile directive into a GitServer
func (gsrv *GitServer) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {

		// Check if we have optional "browse" on the end of 'git_server' directive
		args := d.RemainingArgs()
		switch len(args) {
		case 0:
		case 1:
			if args[0] != "browse" {
				return d.ArgErr()
			}
			gsrv.Browse = true
		default:
			return d.ArgErr()
		}

		// Loop over remaining options
		for d.NextBlock(0) {
			switch d.Val() {
			case "protocol":
				if d.NextArg() {
					if d.Val() == "dumb" || d.Val() == "smart" || d.Val() == "both" {
						gsrv.Protocol = d.Val()
					} else {
						return d.ArgErr()
					}
				} else {
					return d.ArgErr()
				}
			case "root":
				if !d.AllArgs(&gsrv.Root) {
					return d.ArgErr()
				}
			case "browse":
				gsrv.Browse = true
			case "template_dir":
				if !d.AllArgs(&gsrv.TemplateDir) {
					return d.ArgErr()
				}
				// case "mirror":
				// 	gsrv.Mirror = true
				// 	if d.NextArg() {
				// 		gsrv.MirrorRemotes = append(gsrv.MirrorRemotes, d.Val())
				// 	} else {
				// 		return d.ArgErr()
				// 	}
			case "ignore_prefix":
				if !d.AllArgs(&gsrv.IgnorePrefix) {
					return d.ArgErr()
				}
			}
		}
	}
	return nil
}

func (gsrv *GitServer) Provision(ctx caddy.Context) error {

	// Support both protocol by default
	if gsrv.Protocol == "" {
		gsrv.Protocol = "both"
	}

	// Serve the set root by default
	if gsrv.Root == "" {
		gsrv.Root = "{http.vars.root}"
	}

	// Configure and load file_server submodule
	// if gsrv.FileServerRaw == nil {
	// 	// Configure a default file_server if one is not configured
	// 	gsrv.FileServerRaw = []byte("{\"handler\":\"file_server\"}")
	// 	fmt.Printf("using default file_server: %s\n", string(gsrv.FileServerRaw))
	// } else {
	// 	fmt.Printf("using file_server: %s\n", string(gsrv.FileServerRaw))
	// }
	// mod, err := ctx.LoadModule(gsrv, "FileServerRaw")
	fileServerRaw := []byte("{\"root\":\"" + gsrv.Root + "\"}")
	mod, err := ctx.LoadModuleByID("http.handlers.file_server", fileServerRaw)
	if err != nil {
		return fmt.Errorf("loading file_server module: %v", err)
	}
	gsrv.FileServer = mod.(*fileserver.FileServer)

	// Setup a logger to use
	gsrv.logger = ctx.Logger()

	return nil
}

func (gsrv GitServer) Validate() error {
	fmt.Println(gsrv)
	return nil
}

// ServeHTTP implements http.MiddlewareHandler
func (gsrv *GitServer) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	// Get repo path on disk
	repoPath, err := gsrv.getRepoPath(r)
	if err == nil {
		// fmt.Println("found repo", repoPath)

		// Here we try to detect git clients and forward them on to a special git protocol handler.
		// All requests that enter the git client handler will return a response.
		if r.Header.Get("Git-Protocol") != "" || strings.HasPrefix(r.UserAgent(), "git") {
			gsrv.logger.Debug("handling git client",
				zap.String("git_protocol", r.Header.Get("Git-Protocol")),
				zap.String("git_client", r.UserAgent()),
				zap.String("req_path", r.RequestURI),
				zap.String("repo_path", repoPath),
			)

			return gsrv.serveGitClient(repoPath, w, r, next)
		}

		// If browse is enabled we check if the requested repo exists and pawn it off to a browser handler.
		if gsrv.Browse {
			// Redirect /<repo>.git to /<repo>
			requestPath := strings.TrimSuffix(r.URL.Path, "/")
			if strings.HasSuffix(requestPath, ".git") {
				http.Redirect(w, r, strings.TrimSuffix(requestPath, ".git"), http.StatusPermanentRedirect)
				return nil
			}

			// Pass it on to the browse handler
			gsrv.logger.Debug("handling web browser",
				zap.String("repo_path", repoPath),
				zap.String("req_path", r.URL.Path))
			return gsrv.serveGitBrowser(repoPath, w, r, next)
		}
	}

	// We pass on the request if it doesn't contain a git repo
	return next.ServeHTTP(w, r)
}

// Parse caddyfile into middleware
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var gsrv GitServer
	err := gsrv.UnmarshalCaddyfile(h.Dispenser)
	return &gsrv, err
}

func (gsrv *GitServer) getRepoPath(r *http.Request) (string, error) {
	// Update repository list
	repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
	root := repl.ReplaceAll(gsrv.Root, ".")
	gsrv.updateRepositories(root)

	// Check if request path begins with a repo path
	for _, path := range gsrv.repositories {
		if strings.HasPrefix(strings.TrimPrefix(r.URL.Path, "/"), path) {
			return filepath.Join(root, path) + ".git", nil
		}
	}

	return "", fmt.Errorf("repo not found")
}

func (gsrv *GitServer) updateRepositories(root string) {

	rootDir, err := os.Stat(root)
	if err != nil {
		fmt.Println("What? - updateRepositories()", err)
		return
	}

	// If the root has been modified since last time, update the repository list
	modTime := rootDir.ModTime()
	if modTime.After(gsrv.repositoriesLastModified) {
		var newRepos []string
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				fmt.Println("walk error", err)
				return err
			}

			// Right now we determine a git repo by a directory with the '.git' suffix
			if d.IsDir() && filepath.Ext(path) == ".git" {
				// fmt.Println("Found repo", path)
				// Strip root from path
				path = strings.TrimPrefix(path, root)
				// Strip '/' prefix from path
				path = strings.TrimPrefix(path, "/")
				// Strip .git suffix
				path = strings.TrimSuffix(path, ".git")
				newRepos = append(newRepos, path)
				return fs.SkipDir
			}
			return nil
		})

		// Update git server
		gsrv.repositories = newRepos
		gsrv.repositoriesLastModified = modTime
	}
}

// Interface Guards
var (
	_ caddy.Provisioner           = (*GitServer)(nil)
	_ caddyhttp.MiddlewareHandler = (*GitServer)(nil)
	_ caddyfile.Unmarshaler       = (*GitServer)(nil)
)
