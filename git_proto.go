package gitserver

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"go.uber.org/zap"
)

// Serve a git client
func (gs *GitServer) serveGitClient(repoPath string, w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	// Only dumb protocol is implemented at the moment
	return gs.serveGitDumb(repoPath, w, r, next)
}

// Serve dumb git client files. These are generated on-the-fly
func (gs *GitServer) serveGitDumb(repoPath string, w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	// repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)

	// root := repl.ReplaceAll(gs.Root, ".")

	// Detect 'info/refs' and generate and serve
	if strings.HasSuffix(r.URL.Path, "info/refs") {
		// Try to open repo
		repo, err := git.PlainOpen(repoPath)
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, fmt.Errorf("could not load repository"))
		}

		// Log the clone attempt
		gs.logger.Info("git clone attempt",
			zap.String("path", r.RequestURI),
			zap.String("git_repo", repoPath),
			zap.String("git_protocol", r.Header.Get("Git-Protocol")),
			zap.String("git_client", r.UserAgent()),
		)

		var refs []string

		// Collect all heads in repo
		repoHeads, err := repo.Branches()
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, err)
		}
		// Write heads to connection
		repoHeads.ForEach(func(r *plumbing.Reference) error {
			fmt.Fprintf(w, "%s\t%s\n", r.Hash().String(), r.Name().String())
			refs = append(refs, r.String())
			return nil
		})

		// Collect all tags in repo
		repoTags, err := repo.Tags()
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, err)
		}
		// Write tags to connection
		repoTags.ForEach(func(r *plumbing.Reference) error {
			fmt.Fprintf(w, "%s\t%s\n", r.Hash().String(), r.Name().String())
			refs = append(refs, r.String())
			return nil
		})

		gs.logger.Debug("generating dumb info/refs",
			zap.String("git_repo", repoPath),
			zap.String("req_path", r.URL.Path),
			zap.String("refs", strings.Join(refs, ",")),
		)

		// The approach below is without a git library //
		// // Find all ref files in GIT_DIR/refs/*/*
		// refPath := filepath.Join(gitDir, "refs/*/*")
		// refFiles, err := filepath.Glob(refPath)
		// if err != nil {
		// 	return err
		// }

		// // Generate 'info/refs'
		// var infoRefs string
		// for _, s := range refFiles {
		// 	refDirs, refName := filepath.Split(s)
		// 	_, refDir := filepath.Split(strings.TrimSuffix(refDirs, "/"))
		// 	refName = filepath.Join("refs", refDir, refName)
		// 	refHash, err := os.ReadFile(s)
		// 	if err != nil {
		// 		return err
		// 	}
		// 	infoRefs += strings.TrimSpace(string(refHash)) + "\t" + refName + "\n"
		// }

		// // Write info/refs to connection and close it
		// fmt.Fprintf(w, "%s", infoRefs)
		//                                             //
		return nil
	}

	// Detect 'objects/info/packs' and generate and serve
	if strings.HasSuffix(r.URL.Path, "objects/info/packs") {

		// Try to open repo
		_, err := git.PlainOpen(repoPath)
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, err)
		}

		// Get packs in repo
		packFiles, err := filepath.Glob(filepath.Join(repoPath, "objects/pack/*.pack"))
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, err)
		}

		// Write pack file response
		for _, packFile := range packFiles {
			fmt.Fprintf(w, "P %s\n", filepath.Base(packFile))
		}

		return nil
	}

	// Serve the file if it exists
	return gs.FileServer.ServeHTTP(w, r, next)
}
