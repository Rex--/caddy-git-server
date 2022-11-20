package gitserver

import (
	"fmt"
	"net/http"
	"os"
	"strings"

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
	Protocol string `json:"protocol,omitempty"`

	// Path to directory containing bare git repos (<repo>.git)
	Root string `json:"root,omitempty"`

	// Enable repo browser
	Browse   bool   `json:"browse,omitempty"`
	Template string `json:"template,omitempty"`

	// Mirror a git repo
	// Mirror        bool `json:"mirror,omitempty"`
	// MirrorRemotes []string

	// File server module that serves static git files
	// FileServerRaw json.RawMessage        `json:"file_server,omitempty" caddy:"namespace=http.handlers inline_key=handler"`
	FileServer *fileserver.FileServer `json:"-"`

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
			case "template":
				if !d.AllArgs(&gsrv.Template) {
					return d.ArgErr()
				}
				// case "mirror":
				// 	gsrv.Mirror = true
				// 	if d.NextArg() {
				// 		gsrv.MirrorRemotes = append(gsrv.MirrorRemotes, d.Val())
				// 	} else {
				// 		return d.ArgErr()
				// 	}
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

// ServeHTTP implements http.MiddlewareHandler
func (gsrv GitServer) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	// Here we try to detect git clients and forward them on to a special git protocol handler.
	// All requests that enter the git client handler will return a response.
	if r.Header.Get("Git-Protocol") != "" || strings.HasPrefix(r.UserAgent(), "git") {
		gsrv.logger.Debug("handling git client",
			zap.String("git_protocol", r.Header.Get("Git-Protocol")),
			zap.String("git_client", r.UserAgent()),
			zap.String("path", r.RequestURI))

		return gsrv.serveGitClient(w, r, next)
	}

	// If browse is enabled we check if the requested repo exists and pawn it off to a browser handler.
	if gsrv.Browse {
		// Caddy placeholder replacer
		repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)

		// Construct the filesystem path based on our root and the request url
		root := repl.ReplaceAll(gsrv.Root, ".")
		requestPath := strings.TrimSuffix(r.URL.Path, "/")
		repoPath := caddyhttp.SanitizedPathJoin(root, requestPath)

		// Try to find the repo
		repoDir, err := os.Stat(repoPath)
		if err != nil {
			// Try with .git
			if !strings.HasSuffix(repoPath, ".git") {
				repoPath += ".git"
				repoDir, err = os.Stat(repoPath)
				if err != nil {
					repoDir = nil
				}
			} else {
				repoDir = nil
			}
		}

		// redirect /<repo>.git to /<repo> if <repo> exists
		if strings.HasSuffix(requestPath, ".git") {
			http.Redirect(w, r, strings.TrimSuffix(requestPath, ".git"), http.StatusTemporaryRedirect)
			return nil
		}

		// If the repo exists and we try to request it, pass it on to the browse handler
		if repoDir != nil && repoDir.IsDir() {
			gsrv.logger.Debug("handling web client",
				zap.String("git_repo", repoPath),
				zap.String("path", r.RequestURI))
			return gsrv.serveGitBrowser(repoPath, w, r, next)
		}
	}

	// We pass on the request if we don't touch it
	return next.ServeHTTP(w, r)
}

// Parse caddyfile into middleware
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var gsrv GitServer
	err := gsrv.UnmarshalCaddyfile(h.Dispenser)
	return gsrv, err
}

// Interface Guards
var (
	_ caddy.Provisioner           = (*GitServer)(nil)
	_ caddyhttp.MiddlewareHandler = (*GitServer)(nil)
	_ caddyfile.Unmarshaler       = (*GitServer)(nil)
)
